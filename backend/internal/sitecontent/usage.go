package sitecontent

import (
	"fmt"
	"strings"
)

// image is one image field of a document.
type image struct {
	path     string
	value    *string
	label    string // where the owner edits it, for media-in-use messages
	fallback string // public value when the file is gone
	shown    string // what visitors see instead
}

func images(c *Content) []image {
	out := []image{{"identity.photo_url", &c.Identity.PhotoURL, "Site profile › Photo", defaultPhotoURL, "the default photo is shown instead"}}
	for i := range c.Organizations.Items {
		o := &c.Organizations.Items[i]
		name := o.Name
		if name == "" {
			name = fmt.Sprintf("item %d", i+1)
		}
		out = append(out, image{fmt.Sprintf("organizations.items[%d].logo_url", i), &o.LogoURL,
			"Home page › Organisations › " + name + " logo", "", "the name is shown instead"})
	}
	return out
}

// LocalMedia lists every /media/... image URL the document references, once each.
func LocalMedia(c Content) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, im := range images(&c) {
		if u := *im.value; strings.HasPrefix(u, "/media/") && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// DropMedia replaces image URLs listed in missing (the photo with the default
// photo, a logo with "") and returns one warning per field. It is never nil.
func DropMedia(c *Content, missing map[string]bool) []FieldError {
	out := []FieldError{}
	for _, im := range images(c) {
		if *im.value != "" && missing[*im.value] {
			*im.value = im.fallback
			out = append(out, FieldError{Path: im.path, Message: "this image is no longer in the media library; " + im.shown})
		}
	}
	return out
}

// Usage labels the places that show url, e.g. "Site profile › Photo" or
// "Home page › Organisations › UST logo". It is never nil.
func Usage(c Content, url string) []string {
	out := []string{}
	if url == "" {
		return out
	}
	for _, im := range images(&c) {
		if *im.value == url {
			out = append(out, im.label)
		}
	}
	return out
}

// ResumeFilename is the download name for the owner's resume:
// "Sayantan Chakraborty" gives "Sayantan-Chakraborty-Resume.pdf". Runs of
// anything outside ASCII letters and digits become one dash.
func ResumeFilename(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "Resume.pdf"
	}
	return s + "-Resume.pdf"
}
