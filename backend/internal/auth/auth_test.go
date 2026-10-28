package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestFirebaseVerifierRS256(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	var fetches atomic.Int32
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()
	v := &FirebaseVerifier{ProjectID: "proj", JWKSURL: jwks.URL}

	sign := func(k *rsa.PrivateKey, kid string, mut func(jwt.MapClaims)) string {
		now := time.Now()
		c := jwt.MapClaims{"iss": "https://securetoken.google.com/proj", "aud": "proj", "sub": "uid-1",
			"email": "Owner@Cadentic.dev", "email_verified": true, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix()}
		if mut != nil {
			mut(c)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = kid
		s, _ := tok.SignedString(k)
		return s
	}
	ctx := context.Background()

	id, err := v.Verify(ctx, sign(key, "k1", nil))
	if err != nil || id.UID != "uid-1" || id.Email != "owner@cadentic.dev" || !id.EmailVerified {
		t.Fatalf("valid token: %+v %v", id, err)
	}
	cases := map[string]string{
		"wrong key":      sign(other, "k1", nil),
		"wrong audience": sign(key, "k1", func(c jwt.MapClaims) { c["aud"] = "other" }),
		"wrong issuer":   sign(key, "k1", func(c jwt.MapClaims) { c["iss"] = "https://evil" }),
		"expired":        sign(key, "k1", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"no subject":     sign(key, "k1", func(c jwt.MapClaims) { c["sub"] = "" }),
		"unsigned":       "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4In0.",
	}
	for name, tok := range cases {
		if _, err := v.Verify(ctx, tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Forged key ids do not cause a JWKS fetch per request.
	before := fetches.Load()
	for i := 0; i < 20; i++ {
		_, _ = v.Verify(ctx, sign(key, "forged", nil))
	}
	if fetches.Load()-before > 1 {
		t.Fatalf("unknown kid triggered %d JWKS fetches", fetches.Load()-before)
	}
}

func TestAuthorizerRequiresVerifiedEmail(t *testing.T) {
	a := Authorizer{Emails: []string{"owner@cadentic.dev"}}
	if a.IsAdmin(&Identity{UID: "x", Email: "owner@cadentic.dev", EmailVerified: false}) {
		t.Fatal("unverified email granted admin")
	}
	if !a.IsAdmin(&Identity{UID: "x", Email: "owner@cadentic.dev", EmailVerified: true}) {
		t.Fatal("owner denied")
	}
	if a.IsAdmin(&Identity{UID: "x", Email: "someone@cadentic.dev", EmailVerified: true}) {
		t.Fatal("stranger granted admin")
	}
	if (Authorizer{UIDs: []string{"u1"}}).IsAdmin(&Identity{UID: "u1"}) == false {
		t.Fatal("uid allow-list ignored")
	}
}
