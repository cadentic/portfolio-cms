// Command wpimport migrates the owner's old WordPress.com blog into the CMS.
//
//	wpimport fetch   download every published post and page from the
//	                 WordPress.com public API into content/wordpress/raw,
//	                 then run build
//	wpimport build   turn the raw API files into content/wordpress/snapshot.json,
//	                 content/wordpress/seed.sql and content/wordpress/redirects.json,
//	                 and download the images to frontend/public/media/wordpress;
//	                 the seed is also written as backend migration 0003 so
//	                 `docker compose up` loads it
//
// The seed is idempotent; load it with scripts/wordpress-import.sh.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 || (os.Args[1] != "fetch" && os.Args[1] != "build") {
		fmt.Fprintln(os.Stderr, "usage: wpimport fetch|build [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	root := fs.String("root", ".", "repository root; other paths are relative to it")
	site := fs.String("site", "csayantan.wordpress.com", "WordPress.com site")
	outDir := fs.String("out", "content/wordpress", "snapshot, seed and raw API output directory")
	mediaDir := fs.String("media-dir", "frontend/public/media/wordpress", "where images are saved")
	download := fs.Bool("download-media", true, "download images that are not saved yet")
	migration := fs.String("migration", "backend/internal/store/migrations/0003_wordpress_import.sql",
		"also write the seed as this backend migration (skipped if its directory does not exist; empty disables)")
	opt := Options{}
	fs.StringVar(&opt.SiteURL, "site-url", "https://cadentic.dev", "new site origin, for canonical URLs")
	fs.StringVar(&opt.ArticlePath, "article-path", "/blog", "public path prefix for posts")
	fs.StringVar(&opt.PagePath, "page-path", "/", "public path prefix for top-level pages")
	fs.StringVar(&opt.BlogIndex, "blog-index", "/blog", "redirect target for archive, category and tag URLs")
	fs.StringVar(&opt.MediaURL, "media-url", "/media/wordpress", "public URL prefix of the media dir")
	fs.Parse(os.Args[2:])
	opt.Site = *site

	out := filepath.Join(*root, *outDir)
	rawDir := filepath.Join(out, "raw")
	var posts []wpPost
	var err error
	if cmd == "fetch" {
		posts, err = fetchAll(*site, rawDir)
	} else {
		posts, err = loadRaw(rawDir)
	}
	if err != nil {
		log.Fatal(err)
	}

	snap, err := Transform(posts, opt)
	if err != nil {
		log.Fatal(err)
	}
	if err := writeJSON(filepath.Join(out, "snapshot.json"), snap); err != nil {
		log.Fatal(err)
	}
	if err := writeJSON(filepath.Join(out, "redirects.json"), snap.Redirects); err != nil {
		log.Fatal(err)
	}
	var seed bytes.Buffer
	if err := writeSeed(&seed, snap); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "seed.sql"), seed.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	if *migration != "" {
		mig := filepath.Join(*root, *migration)
		if st, err := os.Stat(filepath.Dir(mig)); err == nil && st.IsDir() {
			// The migration runner owns the transaction, so drop ours.
			body := bytes.Replace(seed.Bytes(), []byte("BEGIN;\n"), nil, 1)
			body = bytes.Replace(body, []byte("\nCOMMIT;\n"), []byte("\n"), 1)
			if err := os.WriteFile(mig, body, 0o644); err != nil {
				log.Fatal(err)
			}
			log.Printf("wrote migration %s", mig)
		}
	}

	failed := 0
	if *download {
		for _, m := range snap.Media {
			dest := filepath.Join(*root, *mediaDir, filepath.FromSlash(m.File))
			if err := downloadFile(m.SourceURL, dest); err != nil {
				log.Printf("media %s: %v", m.SourceURL, err)
				failed++
			}
		}
	}
	articles := 0
	for _, it := range snap.Items {
		if it.Kind == "article" {
			articles++
		}
	}
	log.Printf("%d articles, %d pages, %d media (%d failed), %d redirects -> %s",
		articles, len(snap.Items)-articles, len(snap.Media), failed, len(snap.Redirects), out)
}

func writeJSON(name string, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, b.Bytes(), 0o644)
}
