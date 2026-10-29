package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rangedReader is an io.ReadSeekCloser over an object in a bucket. It issues
// a ranged GET from the current offset on the first Read after a Seek, so
// http.ServeContent can serve byte ranges and 304s without full downloads.
type rangedReader struct {
	ctx  context.Context
	get  func(ctx context.Context, offset int64) (io.ReadCloser, error)
	size int64
	off  int64
	body io.ReadCloser
}

func (r *rangedReader) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		b, err := r.get(r.ctx, r.off)
		if err != nil {
			return 0, err
		}
		r.body = b
	}
	n, err := r.body.Read(p)
	r.off += int64(n)
	if err == io.EOF && r.off < r.size {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (r *rangedReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.off + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, errors.New("media: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("media: negative position")
	}
	if abs != r.off && r.body != nil {
		r.body.Close()
		r.body = nil
	}
	r.off = abs
	return abs, nil
}

func (r *rangedReader) Close() error {
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

// rangeHeader asks for the object from offset to its end.
func rangeHeader(req *http.Request, offset int64) {
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
}

// tokenCache refreshes an OAuth access token a few minutes before it expires.
type tokenCache struct {
	mu    sync.Mutex
	tok   string
	until time.Time
	fetch func(ctx context.Context) (token string, ttl time.Duration, err error)
}

func (c *tokenCache) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok != "" && time.Now().Before(c.until) {
		return c.tok, nil
	}
	tok, ttl, err := c.fetch(ctx)
	if err != nil {
		return "", err
	}
	c.tok, c.until = tok, time.Now().Add(ttl-3*time.Minute)
	return tok, nil
}

// statusErr turns a failed bucket response into an error without echoing
// more than a short excerpt of the body.
func statusErr(op string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return fmt.Errorf("media %s: HTTP %d: %s", op, resp.StatusCode, b)
}
