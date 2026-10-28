// Package tokens creates single-use random tokens (stored only as SHA-256
// hashes) and stateless HMAC-signed tokens (unsubscribe links, downloads).
package tokens

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// NewRandom returns a 256-bit URL-safe token and its hash for storage.
func NewRandom() (raw string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw = b64.EncodeToString(b)
	return raw, Hash(raw)
}

func Hash(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// NumericCode returns a uniformly random n-digit code.
func NumericCode(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	r := make([]byte, n)
	if _, err := rand.Read(r); err != nil {
		panic(err)
	}
	for i := range b {
		// 250 is the largest multiple of 10 below 256; re-draw above it to avoid bias.
		for r[i] >= 250 {
			one := make([]byte, 1)
			_, _ = rand.Read(one)
			r[i] = one[0]
		}
		b[i] = digits[int(r[i])%10]
	}
	return string(b)
}

var ErrInvalid = errors.New("invalid or expired token")

// Signer issues compact HMAC-SHA256 tokens: purpose|subject|expiry.
type Signer struct{ Key []byte }

func (s Signer) Sign(purpose, subject string, ttl time.Duration) string {
	exp := "0"
	if ttl > 0 {
		exp = strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	}
	payload := purpose + "|" + subject + "|" + exp
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte(payload))
	return b64.EncodeToString([]byte(payload)) + "." + b64.EncodeToString(mac.Sum(nil))
}

// Verify checks signature, purpose and expiry and returns the subject.
func (s Signer) Verify(purpose, token string) (string, error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", ErrInvalid
	}
	payload, err := b64.DecodeString(p)
	if err != nil {
		return "", ErrInvalid
	}
	got, err := b64.DecodeString(sig)
	if err != nil {
		return "", ErrInvalid
	}
	mac := hmac.New(sha256.New, s.Key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), got) {
		return "", ErrInvalid
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != 3 || parts[0] != purpose {
		return "", ErrInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || (exp != 0 && time.Now().Unix() > exp) {
		return "", ErrInvalid
	}
	return parts[1], nil
}
