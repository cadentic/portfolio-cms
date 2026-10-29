package sitecontent

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/media"
)

// Limit bounds one field. For a number, Min and Max bound the value; for a
// list, Max counts items and Item bounds each string; for text, Max counts
// characters. Required means "required when it applies": the server decides.
type Limit struct {
	Max      int  `json:"max"`
	Min      int  `json:"min,omitempty"`
	Item     int  `json:"item,omitempty"`
	Required bool `json:"required,omitempty"`
}

const (
	maxErrors = 30
	maxBytes  = 256 << 10
)

// limits is keyed by path with "[]" for list elements. It drives both
// Normalize and the admin API.
var limits = map[string]Limit{
	"identity.name":                {Max: 80, Required: true},
	"identity.initials":            {Max: 4, Required: true},
	"identity.short_role":          {Max: 80, Required: true},
	"identity.location":            {Max: 80},
	"identity.city":                {Max: 60},
	"identity.country_code":        {Max: 2},
	"identity.experience_years":    {Min: 1, Max: 60, Required: true},
	"identity.photo_url":           {Max: 300, Required: true},
	"identity.summary":             {Max: 600, Required: true},
	"identity.linkedin_url":        {Max: 300},
	"identity.github_url":          {Max: 300},
	"identity.youtube_url":         {Max: 300},
	"identity.youtube_handle":      {Max: 31},
	"identity.other_links":         {Max: 5},
	"identity.other_links[].label": {Max: 40, Required: true},
	"identity.other_links[].url":   {Max: 300, Required: true},
	"identity.education":           {Max: 10},
	"identity.education[].degree":  {Max: 120, Required: true},
	"identity.education[].school":  {Max: 120, Required: true},
	"identity.education[].years":   {Max: 40},
	"identity.languages":           {Max: 12, Item: 40},

	"navigation.brand_subtitle":    {Max: 40},
	"navigation.resume_label":      {Max: 40},
	"navigation.mega_work.title":   {Max: 40, Required: true},
	"navigation.mega_work.text":    {Max: 120},
	"navigation.mega_videos.title": {Max: 40, Required: true},
	"navigation.mega_videos.text":  {Max: 120},
	"navigation.mega_career.title": {Max: 40, Required: true},
	"navigation.mega_career.text":  {Max: 120},
	"navigation.mega_blog.title":   {Max: 40, Required: true},
	"navigation.mega_blog.text":    {Max: 120},

	"hero.eyebrow":           {Max: 120},
	"hero.title":             {Max: 120, Required: true},
	"hero.title_highlight":   {Max: 120},
	"hero.lead":              {Max: 600, Required: true},
	"hero.primary_cta_label": {Max: 40},
	"hero.resume_cta_label":  {Max: 40},
	"hero.availability_text": {Max: 160, Required: true},
	"hero.metrics":           {Max: 6},
	"hero.metrics[].value":   {Max: 24, Required: true},
	"hero.metrics[].label":   {Max: 120, Required: true},

	"about.label":        {Max: 40, Required: true},
	"about.stamp_kicker": {Max: 40},
	"about.stamp_lines":  {Max: 4, Item: 24},
	"about.stamp_text":   {Max: 200},
	"about.kicker":       {Max: 60},
	"about.heading":      {Max: 160, Required: true},
	"about.paragraphs":   {Max: 4, Item: 1200},
	"about.skills":       {Max: 40, Item: 40},

	"experience.label":                  {Max: 40, Required: true},
	"experience.heading":                {Max: 160, Required: true},
	"experience.intro":                  {Max: 500},
	"experience.cta_label":              {Max: 40},
	"experience.roles":                  {Max: 12},
	"experience.roles[].period":         {Max: 40, Required: true},
	"experience.roles[].title":          {Max: 120, Required: true},
	"experience.roles[].org":            {Max: 120},
	"experience.roles[].points":         {Max: 8, Item: 300},
	"experience.earlier_label":          {Max: 80},
	"experience.earlier_roles":          {Max: 30},
	"experience.earlier_roles[].period": {Max: 40, Required: true},
	"experience.earlier_roles[].title":  {Max: 120, Required: true},
	"experience.earlier_roles[].org":    {Max: 120},

	"work.label":         {Max: 40, Required: true},
	"work.heading":       {Max: 160, Required: true},
	"work.intro":         {Max: 500},
	"work.items":         {Max: 12},
	"work.items[].tag":   {Max: 40},
	"work.items[].title": {Max: 120, Required: true},
	"work.items[].text":  {Max: 400, Required: true},

	"organizations.label":            {Max: 40, Required: true},
	"organizations.heading":          {Max: 160, Required: true},
	"organizations.intro":            {Max: 500},
	"organizations.items":            {Max: 24},
	"organizations.items[].name":     {Max: 80, Required: true},
	"organizations.items[].note":     {Max: 40},
	"organizations.items[].logo_url": {Max: 300},
	"organizations.items[].url":      {Max: 300},

	"expertise.label":         {Max: 40, Required: true},
	"expertise.heading":       {Max: 160, Required: true},
	"expertise.intro":         {Max: 500},
	"expertise.items":         {Max: 9},
	"expertise.items[].title": {Max: 80, Required: true},
	"expertise.items[].text":  {Max: 400},
	"expertise.items[].chips": {Max: 8, Item: 40},

	"videos.label":             {Max: 40, Required: true},
	"videos.heading":           {Max: 160, Required: true},
	"videos.intro":             {Max: 500},
	"videos.channel_cta_label": {Max: 60},
	// youtube_id accepts a pasted link, so its input limit is a link's.
	"videos.featured.youtube_id":   {Max: 300},
	"videos.featured.title":        {Max: 120, Required: true},
	"videos.featured.description":  {Max: 400},
	"videos.featured.published_on": {Max: 10},
	"videos.items":                 {Max: 50},
	"videos.items[].youtube_id":    {Max: 300, Required: true},
	"videos.items[].title":         {Max: 120, Required: true},
	"videos.items[].description":   {Max: 400},
	"videos.items[].published_on":  {Max: 10},
	"videos.page_heading":          {Max: 80, Required: true},
	"videos.page_intro":            {Max: 500},
	"videos.page_cta_label":        {Max: 60},
	"videos.empty_text":            {Max: 300},
	"videos.placeholder_title":     {Max: 80},
	"videos.placeholder_text":      {Max: 200},

	"blog.label":        {Max: 40, Required: true},
	"blog.heading":      {Max: 160, Required: true},
	"blog.intro":        {Max: 500},
	"blog.cta_label":    {Max: 40},
	"blog.page_heading": {Max: 80, Required: true},
	"blog.page_intro":   {Max: 500},
	"blog.empty_text":   {Max: 300},

	"newsletter.label":   {Max: 40},
	"newsletter.heading": {Max: 160, Required: true},
	"newsletter.intro":   {Max: 400},

	"faq.label":     {Max: 40},
	"faq.heading":   {Max: 160, Required: true},
	"faq.items":     {Max: 20},
	"faq.items[].q": {Max: 200, Required: true},
	"faq.items[].a": {Max: 1200, Required: true},

	"contact.label":   {Max: 40, Required: true},
	"contact.heading": {Max: 160, Required: true},
	"contact.intro":   {Max: 500},

	"seo.home_title":         {Max: 70, Required: true},
	"seo.home_description":   {Max: 300, Required: true},
	"seo.videos_title":       {Max: 70, Required: true},
	"seo.videos_description": {Max: 300, Required: true},
	"seo.blog_title":         {Max: 70, Required: true},
	"seo.blog_description":   {Max: 300, Required: true},
	"seo.llms_summary":       {Max: 600, Required: true},
}

// alwaysRequired fields are required even while their section is hidden.
var alwaysRequired = map[string]bool{"videos.page_heading": true, "blog.page_heading": true, "newsletter.heading": true}

// conditional fields are required only when this holds.
var conditional = map[string]func(*Content) bool{
	"hero.availability_text": func(c *Content) bool { return c.Hero.AvailabilityVisible },
	"videos.featured.title":  func(c *Content) bool { return c.Videos.Featured.YouTubeID != "" },
}

var (
	linkFields  = map[string]bool{"identity.linkedin_url": true, "identity.github_url": true, "identity.youtube_url": true, "identity.other_links[].url": true, "organizations.items[].url": true}
	imageFields = map[string]bool{"identity.photo_url": true, "organizations.items[].logo_url": true}
	videoFields = map[string]bool{"videos.featured.youtube_id": true, "videos.items[].youtube_id": true}
	dateFields  = map[string]bool{"videos.featured.published_on": true, "videos.items[].published_on": true}

	handleRE      = regexp.MustCompile(`^@[A-Za-z0-9._-]{3,30}$`)
	countryRE     = regexp.MustCompile(`^[A-Z]{2}$`)
	imagesRE      = regexp.MustCompile(`^/images/[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.(?i:jpe?g|png|webp|avif|gif)$`)
	placeholderRE = regexp.MustCompile(`\{[A-Za-z_]+\}`)
	imageExt      = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".avif": true, ".gif": true}
)

const (
	msgRequired = "required"
	msgLink     = "must be a full https:// address"
	msgImage    = "choose an image from the media library"
	msgVideo    = "enter a YouTube link (youtu.be/…, youtube.com/watch?v=…, /shorts/…) or the 11-character video ID"
)

// Limits returns a copy of the field limits for the admin API.
func Limits() map[string]Limit { return maps.Clone(limits) }

// Normalize cleans c in place (whitespace, empty rows, YouTube links to IDs,
// @handles, upper-case country codes, nil lists to []) and returns every
// problem, at most 30. Paths index the lists as they were passed in.
// Whether new /media images exist is checked by Service.Apply, not here.
func Normalize(c *Content) []FieldError {
	n := &normalizer{c: c}
	v := reflect.ValueOf(c).Elem()
	for i := range v.NumField() {
		sec := v.Field(i)
		hidden := false
		if f := sec.FieldByName("Visible"); f.IsValid() {
			hidden = !f.Bool()
		}
		key := jsonKey(v.Type().Field(i))
		n.walk(sec, key, key, hidden, false)
	}
	if b, err := json.Marshal(c); err != nil || len(b) > maxBytes {
		n.add("", "site content is too large")
	}
	return n.errs
}

type normalizer struct {
	c    *Content
	errs []FieldError
}

func (n *normalizer) add(path, msg string) {
	if len(n.errs) < maxErrors {
		n.errs = append(n.errs, FieldError{Path: path, Message: msg})
	}
}

// walk visits the fields of a struct. p is the reported path, k the limits
// key, hidden whether the section is hidden, row whether v is a list element.
func (n *normalizer) walk(v reflect.Value, p, k string, hidden, row bool) {
	t := v.Type()
	for i := range t.NumField() {
		f := v.Field(i)
		key := jsonKey(t.Field(i))
		fp, fk := p+"."+key, k+"."+key
		switch f.Kind() {
		case reflect.String:
			n.text(f, fp, fk, hidden, row)
		case reflect.Int:
			if l := limits[fk]; f.Int() < int64(l.Min) || f.Int() > int64(l.Max) {
				n.add(fp, fmt.Sprintf("must be between %d and %d", l.Min, l.Max))
			}
		case reflect.Struct:
			n.walk(f, fp, fk, hidden, row)
		case reflect.Slice:
			n.list(f, fp, fk, hidden)
		}
	}
}

func (n *normalizer) list(f reflect.Value, p, k string, hidden bool) {
	l := limits[k]
	out := reflect.MakeSlice(f.Type(), 0, f.Len())
	seen := map[string]int{}
	for i := range f.Len() {
		el := f.Index(i)
		ep := fmt.Sprintf("%s[%d]", p, i)
		if el.Kind() == reflect.String {
			s := clean(el.String())
			if s == "" {
				continue
			}
			if rc := utf8.RuneCountInString(s); l.Item > 0 && rc > l.Item {
				n.add(ep, fmt.Sprintf("at most %d characters (got %d)", l.Item, rc))
			} else {
				n.placeholders(ep, s)
			}
			out = reflect.Append(out, reflect.ValueOf(s))
			continue
		}
		if blank(el) {
			continue
		}
		n.walk(el, ep, k+"[]", hidden, true)
		if k == "videos.items" {
			if id := el.FieldByName("YouTubeID").String(); content.YouTubeID(id) == id && id != "" {
				if j, ok := seen[id]; ok {
					n.add(ep+".youtube_id", fmt.Sprintf("already listed as item %d", j+1))
				} else {
					seen[id] = i
				}
			}
		}
		out = reflect.Append(out, el)
	}
	if out.Len() > l.Max {
		n.add(p, fmt.Sprintf("at most %d items", l.Max))
	}
	f.Set(out)
}

func (n *normalizer) text(f reflect.Value, p, k string, hidden, row bool) {
	s := clean(f.String())
	bad := ""
	switch {
	case videoFields[k] && s != "":
		if id := content.YouTubeID(s); id != "" {
			s = id
		} else {
			bad = msgVideo
		}
	case k == "identity.youtube_handle" && s != "" && !strings.HasPrefix(s, "@"):
		s = "@" + s
	case k == "identity.country_code":
		s = strings.ToUpper(s)
	}
	f.SetString(s)
	if bad != "" {
		n.add(p, bad)
		return
	}
	l := limits[k]
	if s == "" {
		if n.required(k, l, hidden, row) {
			n.add(p, msgRequired)
		}
		return
	}
	if rc := utf8.RuneCountInString(s); rc > l.Max {
		n.add(p, fmt.Sprintf("at most %d characters (got %d)", l.Max, rc))
		return
	}
	switch {
	case linkFields[k]:
		if !httpsURL(s) {
			n.add(p, msgLink)
		}
	case imageFields[k]:
		if !localImage(s) {
			n.add(p, msgImage)
		}
	case videoFields[k]:
	case dateFields[k]:
		if _, err := time.Parse("2006-01-02", s); err != nil {
			n.add(p, "must be a date written YYYY-MM-DD")
		}
	case k == "identity.youtube_handle":
		if !handleRE.MatchString(s) {
			n.add(p, "must be @ followed by 3 to 30 letters, digits, dots, dashes or underscores")
		}
	case k == "identity.country_code":
		if !countryRE.MatchString(s) {
			n.add(p, "must be a two-letter country code, e.g. IN")
		}
	default:
		n.placeholders(p, s)
	}
}

func (n *normalizer) required(k string, l Limit, hidden, row bool) bool {
	if !l.Required {
		return false
	}
	if cond, ok := conditional[k]; ok {
		return cond(n.c)
	}
	return row || !hidden || alwaysRequired[k]
}

func (n *normalizer) placeholders(p, s string) {
	for _, m := range placeholderRE.FindAllString(s, -1) {
		if m != "{years}" {
			n.add(p, "unknown placeholder "+m+"; the only placeholder is {years}")
			return
		}
	}
}

// clean maps line breaks and tabs to spaces, strips control characters and
// bidi overrides, collapses whitespace and trims.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == ' ' || r == ' ':
			return ' '
		case r <= 0x1f || (r >= 0x7f && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// blank reports whether every string and list in a list row is empty.
func blank(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return clean(v.String()) == ""
	case reflect.Slice:
		for i := range v.Len() {
			if !blank(v.Index(i)) {
				return false
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if !blank(v.Field(i)) {
				return false
			}
		}
	}
	return true
}

// httpsURL accepts an https address a browser can open. url.Parse leaves the
// port range and host labels unchecked, and invisible format characters (a
// zero-width space, say) would make the saved text differ from where the link
// goes.
func httpsURL(s string) bool {
	if strings.ContainsAny(s, " \\") || strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Cf, r) }) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	host := u.Hostname()
	if strings.Contains(host, ":") { // an IPv6 literal, which url.Parse checks
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || strings.ContainsFunc(label, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '-' && r != '_'
		}) {
			return false
		}
	}
	return true
}

func localImage(s string) bool {
	if key, ok := strings.CutPrefix(s, "/media/"); ok {
		return media.ValidKey(key) && !strings.HasPrefix(key, "private/") && imageExt[strings.ToLower(path.Ext(key))]
	}
	return imagesRE.MatchString(s) && !strings.Contains(s, "..")
}

func jsonKey(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

// fillNil replaces nil lists with empty ones at every depth.
func fillNil(v reflect.Value) {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		}
		for i := range v.Len() {
			fillNil(v.Index(i))
		}
	case reflect.Struct:
		for i := range v.NumField() {
			fillNil(v.Field(i))
		}
	}
}
