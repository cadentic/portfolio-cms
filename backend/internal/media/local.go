package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
)

// Local keeps objects under one directory. os.Root confines every access to
// it, including through symlinks.
type Local struct {
	root *os.Root
}

func NewLocal(dir string) (*Local, error) {
	if dir == "" {
		dir = "/data/media"
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("media directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("media directory: %w", err)
	}
	return &Local{root: root}, nil
}

func (l *Local) Name() string { return "local" }

func (l *Local) Put(_ context.Context, key, _ string, body io.Reader, _ int64) error {
	if !ValidKey(key) {
		return fmt.Errorf("media: invalid key %q", key)
	}
	if _, err := l.root.Stat(key); err == nil {
		return ErrExists
	}
	if dir := path.Dir(key); dir != "." {
		if err := l.root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	// The leading dot keeps half-written files out of ValidKey's reach.
	tmp := path.Join(path.Dir(key), "."+path.Base(key)+"."+hex.EncodeToString(b))
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = l.root.Rename(tmp, key)
	}
	if err != nil {
		_ = l.root.Remove(tmp)
	}
	return err
}

func (l *Local) Open(_ context.Context, key string) (*Object, error) {
	if !ValidKey(key) {
		return nil, ErrNotFound
	}
	f, err := l.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = ErrNotFound
		}
		return nil, err
	}
	etag := `"` + strconv.FormatInt(st.Size(), 36) + "-" + strconv.FormatInt(st.ModTime().UnixNano(), 36) + `"`
	return &Object{Content: f, Size: st.Size(), ModTime: st.ModTime(), ETag: etag}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	if !ValidKey(key) {
		return ErrNotFound
	}
	err := l.root.Remove(key)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
