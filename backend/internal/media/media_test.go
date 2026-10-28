package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidKey(t *testing.T) {
	good := []string{"wordpress/2011/05/rac.png", "uploads/2026/10/0b1c-photo.jpg", "a", "A_b-c.d/e"}
	bad := []string{"", "/abs.png", "../etc/passwd", "a/../b", "a/./b", "a//b", ".hidden", "a/.x", `a\b`, "a/b/", "a%2Fb",
		"a b", "a\x00b", "é.png", strings.Repeat("a", 513)}
	for _, k := range good {
		if !ValidKey(k) {
			t.Errorf("ValidKey(%q) = false", k)
		}
	}
	for _, k := range bad {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true", k)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		head []byte
		ext  string
	}{
		{[]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), "jpg"},
		{[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "png"},
		{[]byte("GIF89a\x01\x00\x01\x00"), "gif"},
		{[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "webp"},
		{[]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00mif1"), "avif"},
		{[]byte("%PDF-1.7\n"), "pdf"},
		{[]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), "mp4"},
		{[]byte("\x1a\x45\xdf\xa3\x01\x00\x00\x00\x00\x00\x00\x1fB\x86\x81\x01B\xf7\x81\x01B\xf2\x81\x04B\xf3\x81\x08B\x82\x84webm"), "webm"},
	}
	for _, c := range cases {
		typ, ok := Detect(c.head)
		if !ok || typ.Ext != c.ext {
			t.Errorf("Detect(%q) = %v %v, want %s", c.head, typ, ok, c.ext)
		}
	}
	for _, head := range []string{`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"<!DOCTYPE html><html>", "<?xml version=\"1.0\"?><svg/>", "MZ\x90\x00", "PK\x03\x04", "plain text", "\x00\x00\x00\x14ftypqt  "} {
		if typ, ok := Detect([]byte(head)); ok {
			t.Errorf("Detect(%q) accepted as %s", head, typ.MIME)
		}
	}
}

func TestContentTypeAndSlug(t *testing.T) {
	if ct, ok := ContentType("wordpress/2011/06/IBM.JPEG"); ct != "image/jpeg" || !ok {
		t.Errorf("ContentType jpeg = %s %v", ct, ok)
	}
	if ct, ok := ContentType("x/evil.html"); ct != "application/octet-stream" || ok {
		t.Errorf("ContentType html = %s %v", ct, ok)
	}
	for in, want := range map[string]string{
		"My Photo (1).JPG": "my-photo-1", `C:\Users\me\..\Résumé.pdf`: "r-sum", "../../etc/passwd": "passwd",
		"": "file", "....": "file", strings.Repeat("ab", 80) + ".png": strings.Repeat("ab", 30),
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func roundTrip(t *testing.T, s Storage) {
	t.Helper()
	ctx := context.Background()
	data := []byte("0123456789abcdefghij")
	key := "uploads/2026/10/abc-file.png"
	if err := s.Put(ctx, key, "image/png", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Put(ctx, key, "image/png", bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrExists) {
		t.Fatalf("second put: %v, want ErrExists", err)
	}
	obj, err := s.Open(ctx, key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if obj.Size != int64(len(data)) || obj.ETag == "" {
		t.Fatalf("object = %+v", obj)
	}
	if _, err := obj.Content.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 5)
	if _, err := io.ReadFull(obj.Content, part); err != nil || string(part) != "56789" {
		t.Fatalf("ranged read = %q %v", part, err)
	}
	if _, err := obj.Content.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(obj.Content)
	if err != nil || !bytes.Equal(all, data) {
		t.Fatalf("full read = %q %v", all, err)
	}
	obj.Content.Close()
	if _, err := s.Open(ctx, "uploads/missing.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open missing: %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete again: %v", err)
	}
}

func TestLocal(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, l)
	// A symlink planted inside the directory cannot reach outside it.
	outside := filepath.Join(t.TempDir(), "secret.png")
	os.WriteFile(outside, []byte("secret"), 0o600)
	os.Symlink(outside, filepath.Join(dir, "link.png"))
	if _, err := l.Open(context.Background(), "link.png"); err == nil {
		t.Fatal("symlink escape was served")
	}
	if err := l.Put(context.Background(), "../x.png", "image/png", strings.NewReader("x"), 1); err == nil {
		t.Fatal("put outside the root succeeded")
	}
}

// fakeBucket is a tiny in-memory stand-in for both bucket APIs.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
	auth    string
}

func (f *fakeBucket) get(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

func serveRange(w http.ResponseWriter, r *http.Request, b []byte) {
	if rg := r.Header.Get("Range"); rg != "" {
		off, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(b[off:])
		return
	}
	w.Write(b)
}

func TestGCS(t *testing.T) {
	f := &fakeBucket{objects: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(r.Form.Get("assertion"), ".") != 2 {
				http.Error(w, "bad grant", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		case r.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "unauthorised", 401)
			return
		case r.Method == "POST" && r.URL.Path == "/upload/storage/v1/b/bkt/o":
			key := r.URL.Query().Get("name")
			if _, ok := f.get(key); ok && r.URL.Query().Get("ifGenerationMatch") == "0" {
				w.WriteHeader(412)
				return
			}
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.objects[key] = b
			f.mu.Unlock()
			return
		}
		key, ok := strings.CutPrefix(r.URL.EscapedPath(), "/storage/v1/b/bkt/o/")
		if !ok || strings.Contains(key, "/") {
			http.Error(w, "bad path "+r.URL.EscapedPath(), 400)
			return
		}
		key = strings.ReplaceAll(key, "%2F", "/")
		b, found := f.get(key)
		if !found {
			w.WriteHeader(404)
			return
		}
		switch {
		case r.Method == "DELETE":
			f.mu.Lock()
			delete(f.objects, key)
			f.mu.Unlock()
		case r.URL.Query().Get("alt") == "media":
			serveRange(w, r, b)
		default:
			json.NewEncoder(w).Encode(map[string]string{"size": strconv.Itoa(len(b)), "updated": time.Now().Format(time.RFC3339), "generation": "17"})
		}
	}))
	defer srv.Close()

	pk, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(pk)
	creds, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "media@p.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": srv.URL + "/token"})
	g, err := NewGCS("bkt", string(creds), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	g.BaseURL = srv.URL
	roundTrip(t, g)

	if _, err := NewGCS("bkt", `{"type":"authorized_user"}`, srv.Client()); err == nil {
		t.Fatal("user credentials were accepted")
	}
}

func TestAzure(t *testing.T) {
	f := &fakeBucket{objects: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			if r.Header.Get("X-IDENTITY-HEADER") != "idh" || r.URL.Query().Get("client_id") != "cid" ||
				r.URL.Query().Get("resource") != "https://storage.azure.com/" {
				http.Error(w, "bad identity request", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "tok", "expires_on": strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("x-ms-version") == "" {
			http.Error(w, "unauthorised", 403)
			return
		}
		key, ok := strings.CutPrefix(r.URL.Path, "/media/")
		if !ok {
			http.Error(w, "wrong container", 400)
			return
		}
		b, found := f.get(key)
		switch r.Method {
		case "PUT":
			if found && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(409)
				return
			}
			if r.Header.Get("x-ms-blob-type") != "BlockBlob" {
				http.Error(w, "blob type", 400)
				return
			}
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.objects[key] = b
			f.mu.Unlock()
			w.WriteHeader(201)
		case "HEAD", "GET":
			if !found {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("ETag", `"0x8D"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			if r.Method == "HEAD" {
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				return
			}
			serveRange(w, r, b)
		case "DELETE":
			if !found {
				w.WriteHeader(404)
				return
			}
			f.mu.Lock()
			delete(f.objects, key)
			f.mu.Unlock()
			w.WriteHeader(202)
		}
	}))
	defer srv.Close()

	a, err := NewAzure(AzureOptions{Account: "acct", Container: "media", IdentityEndpoint: srv.URL + "/identity", IdentityHeader: "idh", ClientID: "cid"}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	a.BaseURL = srv.URL
	roundTrip(t, a)

	if _, err := NewAzure(AzureOptions{Account: "acct", Container: "media"}, srv.Client()); err == nil {
		t.Fatal("azure without credentials was accepted")
	}
	sas, _ := NewAzure(AzureOptions{Account: "acct", Container: "media", SAS: "?sv=1&sig=x"}, srv.Client())
	if u := sas.blobURL("a/b c.png"); u != "https://acct.blob.core.windows.net/media/a/b%20c.png?sv=1&sig=x" {
		t.Fatalf("sas url = %s", u)
	}
}
