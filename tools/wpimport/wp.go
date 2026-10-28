package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// wpPost mirrors the fields we use from the WordPress.com REST API v1.1
// (https://developer.wordpress.com/docs/api/1.1/get/sites/%24site/posts/).
type wpPost struct {
	ID            int64                   `json:"ID"`
	Date          string                  `json:"date"`
	Modified      string                  `json:"modified"`
	Title         string                  `json:"title"`
	URL           string                  `json:"URL"`
	Content       string                  `json:"content"`
	Excerpt       string                  `json:"excerpt"`
	Slug          string                  `json:"slug"`
	Status        string                  `json:"status"`
	Type          string                  `json:"type"`
	Parent        json.RawMessage         `json:"parent"` // false or {"ID":..}
	MenuOrder     flexInt                 `json:"menu_order"`
	FeaturedImage string                  `json:"featured_image"`
	Author        wpAuthor                `json:"author"`
	Tags          map[string]wpTerm       `json:"tags"`
	Categories    map[string]wpTerm       `json:"categories"`
	Attachments   map[string]wpAttachment `json:"attachments"`
}

type wpAuthor struct {
	Name string `json:"name"`
}

type wpTerm struct {
	ID          int64  `json:"ID"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type wpAttachment struct {
	ID       int64   `json:"ID"`
	URL      string  `json:"URL"`
	MimeType string  `json:"mime_type"`
	Alt      string  `json:"alt"`
	Width    flexInt `json:"width"`
	Height   flexInt `json:"height"`
}

// flexInt accepts numbers sent either as JSON numbers or strings ("600").
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" || s == "false" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil // unknown format: treat as missing rather than failing the import
	}
	*f = flexInt(n)
	return nil
}

type wpPage struct {
	Found int      `json:"found"`
	Posts []wpPost `json:"posts"`
}

// parentID returns the parent post ID, or 0 when the API returned `false`.
func (p wpPost) parentID() int64 {
	var ref struct {
		ID int64 `json:"ID"`
	}
	if len(p.Parent) == 0 || json.Unmarshal(p.Parent, &ref) != nil {
		return 0
	}
	return ref.ID
}

const apiBase = "https://public-api.wordpress.com/rest/v1.1/sites/"

var httpClient = &http.Client{Timeout: 60 * time.Second}

// fetchAll downloads every published post and page of the site and saves
// the raw API pages under rawDir so the import can be re-run offline.
func fetchAll(site, rawDir string) ([]wpPost, error) {
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return nil, err
	}
	var all []wpPost
	for _, typ := range []string{"post", "page"} {
		for page := 1; ; page++ {
			q := url.Values{}
			q.Set("type", typ)
			q.Set("number", "100")
			q.Set("page", strconv.Itoa(page))
			q.Set("order_by", "date")
			q.Set("order", "ASC")
			body, err := getWithRetry(apiBase + url.PathEscape(site) + "/posts/?" + q.Encode())
			if err != nil {
				return nil, fmt.Errorf("fetch %s page %d: %w", typ, page, err)
			}
			var res wpPage
			if err := json.Unmarshal(body, &res); err != nil {
				return nil, fmt.Errorf("decode %s page %d: %w", typ, page, err)
			}
			if len(res.Posts) == 0 {
				break
			}
			name := filepath.Join(rawDir, fmt.Sprintf("%ss-%03d.json", typ, page))
			if err := os.WriteFile(name, indentJSON(body), 0o644); err != nil {
				return nil, err
			}
			all = append(all, res.Posts...)
			if page*100 >= res.Found {
				break
			}
		}
	}
	return all, nil
}

// loadRaw reads API pages previously saved by fetchAll.
func loadRaw(rawDir string) ([]wpPost, error) {
	files, err := filepath.Glob(filepath.Join(rawDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no raw API files in %s; run `wpimport fetch` first", rawDir)
	}
	var all []wpPost
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var res wpPage
		if err := json.Unmarshal(b, &res); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, res.Posts...)
	}
	return all, nil
}

func getWithRetry(u string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "cadentic-portfolio-wpimport/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return body, nil
		}
		lastErr = fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// downloadFile saves u to dest unless dest already exists.
func downloadFile(u, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	body, err := getWithRetry(u)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, body, 0o644)
}

func indentJSON(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return b
	}
	return append(out, '\n')
}
