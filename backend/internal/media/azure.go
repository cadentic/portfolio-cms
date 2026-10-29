package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const azureVersion = "2023-11-03"

type AzureOptions struct {
	Account   string
	Container string
	// SAS is a container SAS token without the leading '?'. When empty, the
	// managed identity (IDENTITY_ENDPOINT/IDENTITY_HEADER) is used.
	SAS              string
	IdentityEndpoint string
	IdentityHeader   string
	ClientID         string // user-assigned identity; empty for system-assigned
}

// Azure talks to the Blob Storage REST API.
type Azure struct {
	BaseURL   string // https://<account>.blob.core.windows.net; tests override it
	container string
	sas       string
	client    *http.Client
	tokens    *tokenCache
}

func NewAzure(o AzureOptions, client *http.Client) (*Azure, error) {
	if o.Account == "" || o.Container == "" {
		return nil, errors.New("MEDIA_AZURE_ACCOUNT and MEDIA_AZURE_CONTAINER are required when MEDIA_STORAGE=azure")
	}
	a := &Azure{BaseURL: "https://" + o.Account + ".blob.core.windows.net", container: o.Container,
		sas: strings.TrimPrefix(strings.TrimSpace(o.SAS), "?"), client: client}
	if a.sas == "" {
		if o.IdentityEndpoint == "" || o.IdentityHeader == "" {
			return nil, errors.New("MEDIA_STORAGE=azure needs a managed identity (IDENTITY_ENDPOINT) or MEDIA_AZURE_SAS")
		}
		a.tokens = &tokenCache{fetch: func(ctx context.Context) (string, time.Duration, error) {
			return managedIdentityToken(ctx, client, o)
		}}
	}
	return a, nil
}

func (a *Azure) Name() string { return "azure" }

func (a *Azure) blobURL(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	u := a.BaseURL + "/" + url.PathEscape(a.container) + "/" + strings.Join(segs, "/")
	if a.sas != "" {
		u += "?" + a.sas
	}
	return u
}

func (a *Azure) do(ctx context.Context, method, key string, body io.Reader, hdr func(*http.Request)) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.blobURL(key), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-ms-version", azureVersion)
	if a.tokens != nil {
		tok, err := a.tokens.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("media azure token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if hdr != nil {
		hdr(req)
	}
	return a.client.Do(req)
}

func (a *Azure) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if !ValidKey(key) {
		return fmt.Errorf("media: invalid key %q", key)
	}
	resp, err := a.do(ctx, http.MethodPut, key, body, func(r *http.Request) {
		r.ContentLength = size
		r.Header.Set("x-ms-blob-type", "BlockBlob")
		r.Header.Set("x-ms-blob-content-type", contentType)
		r.Header.Set("If-None-Match", "*")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed:
		return ErrExists
	case resp.StatusCode >= 300:
		return statusErr("upload", resp)
	}
	return nil
}

func (a *Azure) Open(ctx context.Context, key string) (*Object, error) {
	if !ValidKey(key) {
		return nil, ErrNotFound
	}
	resp, err := a.do(ctx, http.MethodHead, key, nil, nil)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media stat: HTTP %d", resp.StatusCode)
	}
	size, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return nil, errors.New("media stat: no Content-Length")
	}
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `"`) {
		etag = `"` + etag + `"`
	}
	rr := &rangedReader{ctx: ctx, size: size, get: func(ctx context.Context, off int64) (io.ReadCloser, error) {
		resp, err := a.do(ctx, http.MethodGet, key, nil, func(r *http.Request) {
			rangeHeader(r, off)
			r.Header.Set("If-Match", etag) // fail rather than splice two versions
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			defer resp.Body.Close()
			return nil, statusErr("read", resp)
		}
		return resp.Body, nil
	}}
	return &Object{Content: rr, Size: size, ModTime: mod, ETag: etag}, nil
}

func (a *Azure) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrNotFound
	}
	resp, err := a.do(ctx, http.MethodDelete, key, nil, nil)
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

// managedIdentityToken uses the Container Apps / App Service identity
// endpoint (api-version 2019-08-01).
func managedIdentityToken(ctx context.Context, client *http.Client, o AzureOptions) (string, time.Duration, error) {
	q := url.Values{"resource": {"https://storage.azure.com/"}, "api-version": {"2019-08-01"}}
	if o.ClientID != "" {
		q.Set("client_id", o.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.IdentityEndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("X-IDENTITY-HEADER", o.IdentityHeader)
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, statusErr("managed identity token", resp)
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresOn   any    `json:"expires_on"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil || t.AccessToken == "" {
		return "", 0, errors.New("managed identity response has no access_token")
	}
	ttl := time.Hour
	var exp int64
	switch v := t.ExpiresOn.(type) {
	case string:
		exp, _ = strconv.ParseInt(v, 10, 64)
	case float64:
		exp = int64(v)
	}
	if exp > 0 {
		ttl = time.Until(time.Unix(exp, 0))
	}
	return t.AccessToken, ttl, nil
}
