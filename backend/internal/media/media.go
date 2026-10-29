// Package media stores uploaded files in a private bucket (Google Cloud
// Storage or Azure Blob Storage) or, for local runs, on disk. Objects are
// addressed by key, the path after /media/ in public URLs, and are only ever
// served through the API: bucket URLs are never handed to browsers.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/config"
)

var (
	ErrNotFound = errors.New("media: object not found")
	ErrExists   = errors.New("media: object already exists")
)

// Object is an opened stored file. Content seeks lazily, so serving a range
// or a 304 does not download the whole object.
type Object struct {
	Content io.ReadSeekCloser
	Size    int64
	ModTime time.Time
	ETag    string // quoted
}

type Storage interface {
	// Name is "local", "gcs" or "azure".
	Name() string
	// Put writes a new object; it never overwrites (ErrExists).
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	// Open returns ErrNotFound when the key does not exist.
	Open(ctx context.Context, key string) (*Object, error)
	// Delete returns ErrNotFound when the key does not exist.
	Delete(ctx context.Context, key string) error
}

// New builds the storage named by MEDIA_STORAGE.
func New(cfg *config.Config) (Storage, error) {
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
	switch cfg.MediaStorage {
	case "", "local":
		return NewLocal(cfg.MediaLocalDir)
	case "gcs":
		return NewGCS(cfg.MediaGCSBucket, cfg.MediaGCSCredentials, client)
	case "azure":
		return NewAzure(AzureOptions{Account: cfg.MediaAzureAccount, Container: cfg.MediaAzureContainer, SAS: cfg.MediaAzureSAS,
			IdentityEndpoint: cfg.AzureIdentityEndpoint, IdentityHeader: cfg.AzureIdentityHeader, ClientID: cfg.AzureClientID}, client)
	}
	return nil, fmt.Errorf("MEDIA_STORAGE must be local, gcs or azure, not %q", cfg.MediaStorage)
}

// keyRE allows only plain path segments that start with a letter or digit, so
// "..", hidden files, empty segments, backslashes and encoded tricks never
// reach a storage backend.
var keyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// ValidKey reports whether key is safe to pass to a backend.
func ValidKey(key string) bool {
	return len(key) <= 512 && keyRE.MatchString(key)
}

// Type is an accepted upload format.
type Type struct {
	MIME string
	Ext  string
	Kind string // cms_media.kind: image, video or file
}

var types = []Type{
	{"image/jpeg", "jpg", "image"},
	{"image/png", "png", "image"},
	{"image/gif", "gif", "image"},
	{"image/webp", "webp", "image"},
	{"image/avif", "avif", "image"},
	{"application/pdf", "pdf", "file"},
	{"video/mp4", "mp4", "video"},
	{"video/webm", "webm", "video"},
}

// AcceptedFormats is the list shown to the owner when an upload is refused.
const AcceptedFormats = "JPEG, PNG, GIF, WebP, AVIF, PDF, MP4 or WebM"

// Detect identifies a file from its first bytes. The client's file name and
// Content-Type are ignored; SVG and HTML are never accepted because they can
// carry script.
func Detect(head []byte) (Type, bool) {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if b := string(head[8:12]); b == "avif" || b == "avis" {
			return types[4], true
		}
	}
	mime := http.DetectContentType(head)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	if mime == "application/pdf" && !bytes.HasPrefix(head, []byte("%PDF-")) {
		return Type{}, false
	}
	for _, t := range types {
		if t.MIME == mime {
			return t, true
		}
	}
	return Type{}, false
}

// ContentType is the response type for a stored key, from its extension.
// Unknown extensions are served as downloads, never rendered.
func ContentType(key string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(key), "."))
	if ext == "jpeg" {
		ext = "jpg"
	}
	for _, t := range types {
		if t.Ext == ext {
			return t.MIME, true
		}
	}
	return "application/octet-stream", false
}

// Slug turns an uploaded file name into a short, safe key segment.
func Slug(filename string) string {
	name := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "file"
	}
	return s
}
