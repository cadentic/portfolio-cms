package media

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const gcsScope = "https://www.googleapis.com/auth/devstorage.read_write"

// GCS talks to the Cloud Storage JSON API. On Cloud Run the token comes from
// the metadata server (the API's service account); elsewhere from a
// service-account key in MEDIA_GCS_CREDENTIALS.
type GCS struct {
	Bucket   string
	BaseURL  string // https://storage.googleapis.com; tests override it
	client   *http.Client
	tokens   *tokenCache
	metadata string // metadata server origin; tests override it
}

func NewGCS(bucket, credentialsJSON string, client *http.Client) (*GCS, error) {
	if bucket == "" {
		return nil, errors.New("MEDIA_GCS_BUCKET is required when MEDIA_STORAGE=gcs")
	}
	g := &GCS{Bucket: bucket, BaseURL: "https://storage.googleapis.com", client: client, metadata: "http://metadata.google.internal"}
	if strings.TrimSpace(credentialsJSON) != "" {
		key, err := parseServiceAccount(credentialsJSON)
		if err != nil {
			return nil, err
		}
		g.tokens = &tokenCache{fetch: func(ctx context.Context) (string, time.Duration, error) { return key.token(ctx, client) }}
	} else {
		g.tokens = &tokenCache{fetch: g.metadataToken}
	}
	return g, nil
}

func (g *GCS) Name() string { return "gcs" }

func (g *GCS) objectURL(key string) string {
	return g.BaseURL + "/storage/v1/b/" + url.PathEscape(g.Bucket) + "/o/" + url.PathEscape(key)
}

func (g *GCS) do(ctx context.Context, method, u string, body io.Reader, hdr func(*http.Request)) (*http.Response, error) {
	tok, err := g.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("media gcs token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if hdr != nil {
		hdr(req)
	}
	return g.client.Do(req)
}

func (g *GCS) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if !ValidKey(key) {
		return fmt.Errorf("media: invalid key %q", key)
	}
	q := url.Values{"uploadType": {"media"}, "name": {key}, "ifGenerationMatch": {"0"}}
	u := g.BaseURL + "/upload/storage/v1/b/" + url.PathEscape(g.Bucket) + "/o?" + q.Encode()
	resp, err := g.do(ctx, http.MethodPost, u, body, func(r *http.Request) {
		r.Header.Set("Content-Type", contentType)
		r.ContentLength = size
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return ErrExists
	case resp.StatusCode >= 300:
		return statusErr("upload", resp)
	}
	return nil
}

func (g *GCS) Open(ctx context.Context, key string) (*Object, error) {
	if !ValidKey(key) {
		return nil, ErrNotFound
	}
	resp, err := g.do(ctx, http.MethodGet, g.objectURL(key), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr("stat", resp)
	}
	var meta struct {
		Size       string    `json:"size"`
		Updated    time.Time `json:"updated"`
		Generation string    `json:"generation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("media stat: %w", err)
	}
	size, err := strconv.ParseInt(meta.Size, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("media stat: size %q", meta.Size)
	}
	gen := meta.Generation
	u := g.objectURL(key) + "?alt=media&generation=" + url.QueryEscape(gen)
	rr := &rangedReader{ctx: ctx, size: size, get: func(ctx context.Context, off int64) (io.ReadCloser, error) {
		resp, err := g.do(ctx, http.MethodGet, u, nil, func(r *http.Request) { rangeHeader(r, off) })
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			defer resp.Body.Close()
			return nil, statusErr("read", resp)
		}
		return resp.Body, nil
	}}
	return &Object{Content: rr, Size: size, ModTime: meta.Updated, ETag: `"` + gen + `"`}, nil
}

func (g *GCS) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrNotFound
	}
	resp, err := g.do(ctx, http.MethodDelete, g.objectURL(key), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 300:
		return statusErr("delete", resp)
	}
	return nil
}

func (g *GCS) metadataToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.metadata+"/computeMetadata/v1/instance/service-accounts/default/token?scopes="+url.QueryEscape(gcsScope), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("metadata server (set MEDIA_GCS_CREDENTIALS outside Google Cloud): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, statusErr("metadata token", resp)
	}
	return decodeToken(resp.Body)
}

func decodeToken(r io.Reader) (string, time.Duration, error) {
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(r).Decode(&t); err != nil || t.AccessToken == "" {
		return "", 0, errors.New("token response has no access_token")
	}
	return t.AccessToken, time.Duration(t.ExpiresIn) * time.Second, nil
}

type serviceAccount struct {
	email    string
	tokenURI string
	key      *rsa.PrivateKey
}

func parseServiceAccount(js string) (*serviceAccount, error) {
	var sa struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal([]byte(js), &sa); err != nil {
		return nil, errors.New("MEDIA_GCS_CREDENTIALS is not valid service-account JSON")
	}
	if sa.Type != "service_account" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("MEDIA_GCS_CREDENTIALS must be a service-account key (type service_account)")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("MEDIA_GCS_CREDENTIALS: private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok {
		return nil, errors.New("MEDIA_GCS_CREDENTIALS: private_key is not an RSA key")
	}
	return &serviceAccount{email: sa.ClientEmail, tokenURI: sa.TokenURI, key: key}, nil
}

// token exchanges a self-signed JWT for an access token (RFC 7523).
func (sa *serviceAccount) token(ctx context.Context, client *http.Client) (string, time.Duration, error) {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	unsigned := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		enc(map[string]any{"iss": sa.email, "scope": gcsScope, "aud": sa.tokenURI, "iat": now, "exp": now + 3600})
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(nil, sa.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", 0, err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion": {unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sa.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, statusErr("service-account token", resp)
	}
	return decodeToken(resp.Body)
}
