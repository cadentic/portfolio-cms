package sitecontent

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// eachString calls fn for every string in v with its path and json key.
func eachString(v reflect.Value, path, key string, fn func(path, key string, s string)) {
	switch v.Kind() {
	case reflect.String:
		fn(path, key, v.String())
	case reflect.Slice:
		for i := range v.Len() {
			eachString(v.Index(i), fmt.Sprintf("%s[%d]", path, i), key, fn)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			k := jsonKey(v.Type().Field(i))
			eachString(v.Field(i), strings.TrimPrefix(path+"."+k, "."), k, fn)
		}
	}
}

func TestDefaultsValid(t *testing.T) {
	d := Default()
	if errs := Normalize(&d); len(errs) > 0 {
		t.Fatalf("Normalize(Default()) = %v", errs)
	}
	if !reflect.DeepEqual(d, Default()) {
		a, _ := json.Marshal(d)
		b, _ := json.Marshal(Default())
		t.Fatalf("Normalize changed the defaults:\n%s\n%s", a, b)
	}
}

func TestDefaultsAreCurrentSite(t *testing.T) {
	e := Expand(Default())
	if got := e.Hero.Metrics[0].Value; got != "11+ years" {
		t.Errorf("metrics[0] = %q", got)
	}
	if !strings.HasPrefix(e.FAQ.Items[1].A, "More than 11 years") {
		t.Errorf("faq[1] = %q", e.FAQ.Items[1].A)
	}
	for name, got := range map[string]string{
		"summary":     e.Identity.Summary,
		"lead":        e.Hero.Lead,
		"description": e.SEO.HomeDescription,
		"llms":        e.SEO.LLMsSummary,
	} {
		if !strings.Contains(got, "11+ years") || strings.Contains(got, "{") {
			t.Errorf("%s = %q", name, got)
		}
	}
	if !strings.HasPrefix(e.Hero.Lead, "I'm Sayantan Chakraborty. For 11+ years I've run Oracle RAC") {
		t.Errorf("lead = %q", e.Hero.Lead)
	}
	if !strings.Contains(e.SEO.LLMsSummary, "(11+ years, Bangalore, India)") {
		t.Errorf("llms = %q", e.SEO.LLMsSummary)
	}
	d := Default()
	if d.Videos.Featured.YouTubeID != "gXT9izr0ljQ" || len(d.Videos.Items) != 1 || d.Videos.Items[0].YouTubeID != "gXT9izr0ljQ" {
		t.Errorf("videos = %+v / %+v", d.Videos.Featured, d.Videos.Items)
	}
	for name, n := range map[string][2]int{
		"organisations": {len(d.Organizations.Items), 5},
		"roles":         {len(d.Experience.Roles), 4},
		"earlier roles": {len(d.Experience.EarlierRoles), 8},
		"faqs":          {len(d.FAQ.Items), 5},
		"skills":        {len(d.About.Skills), 16},
		"work":          {len(d.Work.Items), 6},
		"expertise":     {len(d.Expertise.Items), 3},
		"metrics":       {len(d.Hero.Metrics), 3},
	} {
		if n[0] != n[1] {
			t.Errorf("%s: %d, want %d", name, n[0], n[1])
		}
	}
	if d.Identity.PhotoURL != "/images/sayantan.jpg" {
		t.Errorf("photo = %q", d.Identity.PhotoURL)
	}
	var names []string
	for _, o := range d.Organizations.Items {
		names = append(names, o.Name)
		if o.LogoURL != "" || o.URL != "" {
			t.Errorf("org %q has a logo or link", o.Name)
		}
	}
	for _, want := range []string{"UST", "Equifax", "IIT Delhi", "Ndedge Informatics", "NIEPA"} {
		if !slices.Contains(names, want) {
			t.Errorf("organisation %q missing", want)
		}
	}
	if d.Navigation.MegaCareer.Text != "Fifteen years from EBS to DRE." {
		t.Errorf("mega career = %q", d.Navigation.MegaCareer.Text)
	}
}

func TestYearsPlaceholderFields(t *testing.T) {
	var got []string
	d := Default()
	eachString(reflect.ValueOf(d), "", "", func(p, _ string, s string) {
		if strings.Contains(s, "{years}") {
			got = append(got, p)
		}
	})
	want := []string{"identity.summary", "hero.lead", "hero.metrics[0].value", "faq.items[1].a", "seo.home_description", "seo.llms_summary"}
	if !slices.Equal(got, want) {
		t.Errorf("{years} in %v, want %v", got, want)
	}
}

func goldenJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func noNull(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case nil:
		t.Errorf("%s is null", path)
	case map[string]any:
		for k, e := range x {
			noNull(t, path+"."+k, e)
		}
	case []any:
		for i, e := range x {
			noNull(t, fmt.Sprintf("%s[%d]", path, i), e)
		}
	}
}

func TestGolden(t *testing.T) {
	for name, v := range map[string]Content{
		"defaults.golden.json": Default(),
		"public.golden.json":   Expand(Default()),
	} {
		file := filepath.Join("testdata", name)
		got := goldenJSON(v)
		if *update {
			if err := os.WriteFile(file, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/sitecontent -run TestGolden -update)", err)
		}
		var a, b any
		if err := json.Unmarshal(got, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s is out of date (run go test ./internal/sitecontent -run TestGolden -update)", name)
		}
		if bytes.Contains(want, []byte("null")) {
			t.Errorf("%s contains null", name)
		}
		noNull(t, name, b)
	}
}

// limitKeys walks the type and returns the limits key of every string,
// number and list field.
func limitKeys(t reflect.Type, key string, out map[string]reflect.Kind) {
	for i := range t.NumField() {
		f := t.Field(i)
		k := strings.TrimPrefix(key+"."+jsonKey(f), ".")
		switch f.Type.Kind() {
		case reflect.String, reflect.Int:
			out[k] = f.Type.Kind()
		case reflect.Struct:
			limitKeys(f.Type, k, out)
		case reflect.Slice:
			out[k] = f.Type.Elem().Kind()
			if f.Type.Elem().Kind() == reflect.Struct {
				limitKeys(f.Type.Elem(), k+"[]", out)
			}
		}
	}
}

func TestLimitsCoverEveryField(t *testing.T) {
	fields := map[string]reflect.Kind{}
	limitKeys(reflect.TypeFor[Content](), "", fields)
	lim := Limits()
	for k, kind := range fields {
		l, ok := lim[k]
		switch {
		case !ok:
			t.Errorf("no limit for %s", k)
		case l.Max <= 0:
			t.Errorf("%s: max %d", k, l.Max)
		case kind != reflect.String && l.Item != 0:
			t.Errorf("%s: item limit on a field that is not a string list", k)
		}
	}
	for k := range lim {
		if _, ok := fields[k]; !ok {
			t.Errorf("limit %s matches no field", k)
		}
	}
	// String lists need a per-item limit.
	for _, k := range []string{"identity.languages", "about.stamp_lines", "about.paragraphs", "about.skills", "experience.roles[].points", "expertise.items[].chips"} {
		if lim[k].Item == 0 {
			t.Errorf("%s: no item limit", k)
		}
	}
	if l := lim["identity.experience_years"]; l != (Limit{Min: 1, Max: 60, Required: true}) {
		t.Errorf("experience_years = %+v", l)
	}
	if l := lim["hero.metrics[].value"]; l != (Limit{Max: 24, Required: true}) {
		t.Errorf("metrics value = %+v", l)
	}
	if l := lim["hero.metrics"]; l != (Limit{Max: 6}) {
		t.Errorf("metrics = %+v", l)
	}
	if l := lim["about.skills"]; l != (Limit{Max: 40, Item: 40}) {
		t.Errorf("skills = %+v", l)
	}
	lim["hero.title"] = Limit{}
	if limits["hero.title"].Max != 120 {
		t.Error("Limits does not return a copy")
	}
	b, _ := json.Marshal(Limit{Max: 6})
	if string(b) != `{"max":6}` {
		t.Errorf("Limit json = %s", b)
	}
}

func TestSectionsMatchContent(t *testing.T) {
	var keys []string
	ct := reflect.TypeFor[Content]()
	for i := range ct.NumField() {
		keys = append(keys, jsonKey(ct.Field(i)))
	}
	if !slices.Equal(keys, Sections) {
		t.Errorf("Sections = %v, Content = %v", Sections, keys)
	}
}

// The merge resets top-level lists and objects only, so nested objects of a
// section must not hold lists (list elements may).
func TestNoListInsideNestedObject(t *testing.T) {
	var hasSlice func(reflect.Type) bool
	hasSlice = func(t reflect.Type) bool {
		for i := range t.NumField() {
			switch f := t.Field(i).Type; f.Kind() {
			case reflect.Slice:
				return true
			case reflect.Struct:
				if hasSlice(f) {
					return true
				}
			}
		}
		return false
	}
	ct := reflect.TypeFor[Content]()
	for i := range ct.NumField() {
		st := ct.Field(i).Type
		for j := range st.NumField() {
			if f := st.Field(j); f.Type.Kind() == reflect.Struct && hasSlice(f.Type) {
				t.Errorf("%s.%s holds a list", Sections[i], jsonKey(f))
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	type tc struct {
		name  string
		edit  func(c *Content)
		path  string // expected first error path; "ok" for none
		msg   string // expected substring of that error
		check func(t *testing.T, c Content)
	}
	var cases []tc
	for _, in := range []string{"https://youtu.be/gXT9izr0ljQ?si=XKDH-MSqA-o66lk3", "https://www.youtube.com/watch?v=gXT9izr0ljQ&t=10",
		"https://youtube.com/shorts/gXT9izr0ljQ", "gXT9izr0ljQ", "  gXT9izr0ljQ\n"} {
		cases = append(cases, tc{name: "youtube " + in, edit: func(c *Content) {
			c.Videos.Featured.YouTubeID = in
			c.Videos.Items[0].YouTubeID = in
		}, path: "ok", check: func(t *testing.T, c Content) {
			if c.Videos.Featured.YouTubeID != "gXT9izr0ljQ" || c.Videos.Items[0].YouTubeID != "gXT9izr0ljQ" {
				t.Errorf("ids = %q, %q", c.Videos.Featured.YouTubeID, c.Videos.Items[0].YouTubeID)
			}
		}})
	}
	for _, in := range []string{"https://vimeo.com/1", "gXT9izr0lj"} {
		cases = append(cases, tc{name: "bad youtube " + in, edit: func(c *Content) { c.Videos.Featured.YouTubeID = in },
			path: "videos.featured.youtube_id", msg: "enter a YouTube link"})
	}
	for _, in := range []string{"/media/uploads/2026/10/x-logo.png", "/media/wordpress/2011/06/ibm.jpg", "/images/sayantan.jpg", "/images/x.WEBP"} {
		cases = append(cases, tc{name: "image " + in, edit: func(c *Content) {
			c.Identity.PhotoURL = in
			c.Organizations.Items[0].LogoURL = in
		}, path: "ok"})
	}
	for _, in := range []string{"https://cdn.example/x.png", "//evil/x.png", "data:image/png;base64,AA", "/media/../x.png",
		"/images/../x.jpg", "/images/x.svg", "/media/x.svg", "/media/private/resume/x.png", "/media/x.png?v=1", "/images/a/b.png"} {
		cases = append(cases, tc{name: "bad image " + in, edit: func(c *Content) { c.Organizations.Items[0].LogoURL = in },
			path: "organizations.items[0].logo_url", msg: "choose an image from the media library"})
	}
	cases = append(cases, tc{name: "link", edit: func(c *Content) { c.Identity.LinkedInURL = "https://www.linkedin.com/in/x" }, path: "ok"})
	for _, in := range []string{"http://linkedin.com/in/x", "javascript:alert(1)", "https://u:p@x.com", "linkedin.com/in/x", "https://", "https://x.com/a b"} {
		cases = append(cases, tc{name: "bad link " + in, edit: func(c *Content) { c.Identity.LinkedInURL = in },
			path: "identity.linkedin_url", msg: "must be a full https:// address"})
	}
	cases = append(cases,
		tc{name: "org link", edit: func(c *Content) { c.Organizations.Items[1].URL = "ftp://x" }, path: "organizations.items[1].url", msg: "https://"},
		tc{name: "other link", edit: func(c *Content) { c.Identity.OtherLinks = []Link{{Label: "Blog", URL: "http://x"}} }, path: "identity.other_links[0].url", msg: "https://"},
		tc{name: "text", edit: func(c *Content) {
			c.Identity.Name = "a\nb"
			c.Identity.City = "a\tb"
			c.Identity.Location = "x‮y⁦z \u0007 w  \r\n "
			c.About.Skills = []string{"  Data Guard  "}
		}, path: "ok", check: func(t *testing.T, c Content) {
			if c.Identity.Name != "a b" || c.Identity.City != "a b" || c.Identity.Location != "xyz w" || c.About.Skills[0] != "Data Guard" {
				t.Errorf("got %q %q %q %q", c.Identity.Name, c.Identity.City, c.Identity.Location, c.About.Skills[0])
			}
		}},
		tc{name: "long name", edit: func(c *Content) { c.Identity.Name = strings.Repeat("é", 81) }, path: "identity.name", msg: "at most 80 characters (got 81)"},
		tc{name: "long skill", edit: func(c *Content) { c.About.Skills = append(c.About.Skills, strings.Repeat("x", 41)) }, path: "about.skills[16]", msg: "at most 40 characters (got 41)"},
		tc{name: "blank rows", edit: func(c *Content) {
			c.Hero.Metrics = append([]Metric{{Value: " ", Label: "\n"}}, c.Hero.Metrics...)
			c.Experience.Roles = append(c.Experience.Roles, Role{Points: []string{"", "  "}})
			c.Identity.Languages = []string{"", "English", " "}
			c.Identity.OtherLinks = nil
			c.Identity.Education = nil
			c.Expertise.Items[0].Chips = nil
		}, path: "ok", check: func(t *testing.T, c Content) {
			if len(c.Hero.Metrics) != 3 || len(c.Experience.Roles) != 4 || !slices.Equal(c.Identity.Languages, []string{"English"}) {
				t.Errorf("rows not dropped: %d metrics, %d roles, %q", len(c.Hero.Metrics), len(c.Experience.Roles), c.Identity.Languages)
			}
			if c.Identity.OtherLinks == nil || c.Identity.Education == nil || c.Expertise.Items[0].Chips == nil {
				t.Error("nil list kept")
			}
		}},
		tc{name: "paths index the input", edit: func(c *Content) { c.Hero.Metrics = []Metric{{}, {Value: "x"}} },
			path: "hero.metrics[1].label", msg: "required"},
		tc{name: "too many metrics", edit: func(c *Content) {
			for range 4 {
				c.Hero.Metrics = append(c.Hero.Metrics, Metric{Value: "1", Label: "a"})
			}
		}, path: "hero.metrics", msg: "at most 6 items"},
		tc{name: "too many organisations", edit: func(c *Content) {
			for i := range 20 {
				c.Organizations.Items = append(c.Organizations.Items, Organization{Name: fmt.Sprint("Org ", i)})
			}
		}, path: "organizations.items", msg: "at most 24 items"},
		tc{name: "duplicate video", edit: func(c *Content) {
			c.Videos.Items = append(c.Videos.Items, Video{YouTubeID: "https://youtu.be/gXT9izr0ljQ", Title: "Again"})
		}, path: "videos.items[1].youtube_id", msg: "already listed as item 1"},
		tc{name: "video row needs an id", edit: func(c *Content) { c.Videos.Items = append(c.Videos.Items, Video{Title: "No id"}) },
			path: "videos.items[1].youtube_id", msg: "required"},
		tc{name: "formats", edit: func(c *Content) {
			c.Identity.YouTubeHandle = "csayantan"
			c.Identity.CountryCode = "in"
			c.Videos.Featured.PublishedOn = "2026-10-28"
		}, path: "ok", check: func(t *testing.T, c Content) {
			if c.Identity.YouTubeHandle != "@csayantan" || c.Identity.CountryCode != "IN" {
				t.Errorf("got %q %q", c.Identity.YouTubeHandle, c.Identity.CountryCode)
			}
		}},
		tc{name: "bad handle", edit: func(c *Content) { c.Identity.YouTubeHandle = "@a b" }, path: "identity.youtube_handle", msg: "@"},
		tc{name: "bad country", edit: func(c *Content) { c.Identity.CountryCode = "IND" }, path: "identity.country_code", msg: ""},
		tc{name: "bad date", edit: func(c *Content) { c.Videos.Featured.PublishedOn = "2026-13-01" }, path: "videos.featured.published_on", msg: "YYYY-MM-DD"},
		tc{name: "years 0", edit: func(c *Content) { c.Identity.ExperienceYears = 0 }, path: "identity.experience_years", msg: "must be between 1 and 60"},
		tc{name: "years 61", edit: func(c *Content) { c.Identity.ExperienceYears = 61 }, path: "identity.experience_years", msg: "must be between 1 and 60"},
		tc{name: "years placeholder", edit: func(c *Content) { c.Hero.Title = "{years} years" }, path: "ok"},
		tc{name: "unknown placeholder", edit: func(c *Content) { c.Hero.Lead = "For {year} years" }, path: "hero.lead",
			msg: "unknown placeholder {year}; the only placeholder is {years}"},
		tc{name: "placeholder in a list", edit: func(c *Content) { c.About.Skills[2] = "{name}" }, path: "about.skills[2]", msg: "unknown placeholder {name}"},
		tc{name: "hidden about without heading", edit: func(c *Content) { c.About.Visible = false; c.About.Heading = ""; c.About.Label = "" }, path: "ok"},
		tc{name: "featured without id or title", edit: func(c *Content) { c.Videos.Featured = Video{} }, path: "ok"},
		tc{name: "visible about without heading", edit: func(c *Content) { c.About.Heading = "" }, path: "about.heading", msg: "required"},
		tc{name: "featured id without title", edit: func(c *Content) { c.Videos.Featured.Title = "" }, path: "videos.featured.title", msg: "required"},
		tc{name: "availability without text", edit: func(c *Content) { c.Hero.AvailabilityText = "" }, path: "hero.availability_text", msg: "required"},
		tc{name: "availability off without text", edit: func(c *Content) { c.Hero.AvailabilityVisible = false; c.Hero.AvailabilityText = "" }, path: "ok"},
		tc{name: "page heading required while hidden", edit: func(c *Content) { c.Videos.Visible = false; c.Videos.PageHeading = "" },
			path: "videos.page_heading", msg: "required"},
		tc{name: "list rows required while hidden", edit: func(c *Content) { c.Organizations.Visible = false; c.Organizations.Items[0].Name = "" },
			path: "organizations.items[0].name", msg: "required"},
		tc{name: "too large", edit: func(c *Content) {
			c.FAQ.Items = nil
			for range 20 {
				c.FAQ.Items = append(c.FAQ.Items, QA{Q: strings.Repeat("&", 200), A: strings.Repeat("&", 1200)})
			}
			for i := range c.Experience.Roles {
				c.Experience.Roles[i].Points = slices.Repeat([]string{strings.Repeat("<", 300)}, 8)
			}
			for range 8 {
				c.Experience.Roles = append(c.Experience.Roles, Role{Period: "p", Title: "t", Points: slices.Repeat([]string{strings.Repeat(">", 300)}, 8)})
			}
		}, path: "", msg: "site content is too large"},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.edit(&c)
			errs := Normalize(&c)
			if tc.path == "ok" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors %v", errs)
				}
			} else {
				if len(errs) == 0 {
					t.Fatalf("no error, want %s: %s", tc.path, tc.msg)
				}
				if errs[0].Path != tc.path || !strings.Contains(errs[0].Message, tc.msg) {
					t.Fatalf("errors %v, want %s: %s", errs, tc.path, tc.msg)
				}
			}
			if tc.check != nil {
				tc.check(t, c)
			}
		})
	}

	t.Run("error format and cap", func(t *testing.T) {
		c := Default()
		c.Identity.Name = strings.Repeat("x", 81)
		errs := Normalize(&c)
		if got := (&ValidationError{Errors: errs}).Error(); got != "identity.name: at most 80 characters (got 81)" {
			t.Errorf("Error() = %q", got)
		}
		c = Default()
		c.About.Skills = slices.Repeat([]string{strings.Repeat("x", 41)}, 40)
		if errs := Normalize(&c); len(errs) != 30 {
			t.Errorf("%d errors, want the cap of 30", len(errs))
		}
	})
}

func TestFromStoredForwardCompatible(t *testing.T) {
	b, _ := json.Marshal(Default())
	var m map[string]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	delete(m["about"], "visible")
	delete(m["hero"], "availability_visible")
	delete(m["identity"], "experience_years")
	delete(m["navigation"], "mega_blog")
	m["hero"]["metrics"] = []any{map[string]any{"value": "1", "label": "a"}}
	m["hero"]["future_field"] = "ignored"
	m["identity"]["other_links"] = nil
	m["future_section"] = map[string]any{"x": 1}
	raw, _ := json.Marshal(m)
	c, err := FromStored(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.About.Visible || !c.Hero.AvailabilityVisible || c.Identity.ExperienceYears != 11 || c.Navigation.MegaBlog != Default().Navigation.MegaBlog {
		t.Errorf("defaults not kept: %v %v %d %+v", c.About.Visible, c.Hero.AvailabilityVisible, c.Identity.ExperienceYears, c.Navigation.MegaBlog)
	}
	if !slices.Equal(c.Hero.Metrics, []Metric{{Value: "1", Label: "a"}}) {
		t.Errorf("metrics = %+v", c.Hero.Metrics)
	}
	if c.Identity.OtherLinks == nil {
		t.Error("nil list")
	}

	// A section that is not an object keeps its default.
	c, err = FromStored([]byte(`{"hero":5,"about":{"heading":"X"}}`))
	if err != nil || !reflect.DeepEqual(c.Hero, Default().Hero) || c.About.Heading != "X" {
		t.Errorf("got %+v, %v", c.Hero, err)
	}
	// A stored list never decodes into the default elements.
	c, _ = FromStored([]byte(`{"organizations":{"items":[{"name":"Acme"}]}}`))
	if len(c.Organizations.Items) != 1 || c.Organizations.Items[0] != (Organization{Name: "Acme"}) {
		t.Errorf("items = %+v", c.Organizations.Items)
	}
	if c, err := FromStored(nil); err != nil || !reflect.DeepEqual(c, Default()) {
		t.Errorf("FromStored(nil) = %v", err)
	}
	if _, err := FromStored([]byte(`[]`)); err == nil {
		t.Error("a stored array was accepted")
	}
}

func sections(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestApplySections(t *testing.T) {
	base := Default()
	for in, want := range map[string]string{
		`{"foo":{}}`:                                 `unknown section "foo"`,
		`{"hero":{"foo":1}}`:                         `hero: unknown field "foo"`,
		`{"hero":{"Title":"x"}}`:                     `hero: unknown field "Title"`,
		`{"hero":{"metrics":[{"value":"1","x":2}]}}`: `hero: unknown field "x"`,
		`{"hero":null}`:                              `hero: must be an object`,
		`{"hero":[]}`:                                `hero: must be an object`,
		`{"identity":{"experience_years":"12"}}`:     `identity.experience_years: must be a whole number`,
		`{"identity":{"experience_years":12.5}}`:     `identity.experience_years: must be a whole number`,
		`{"hero":{"title":5}}`:                       `hero.title: must be text`,
		`{"about":{"visible":"yes"}}`:                `about.visible: must be true or false`,
		`{"hero":{"metrics":{}}}`:                    `hero.metrics: must be a list`,
		`{"videos":{"featured":"x"}}`:                `videos.featured: must be an object`,
	} {
		_, errs := ApplySections(base, sections(t, in))
		if got := (&ValidationError{Errors: errs}).Error(); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}

	c, errs := ApplySections(base, sections(t, `{"hero":{"title":"X"}}`))
	if len(errs) > 0 || c.Hero.Title != "X" {
		t.Fatalf("%v %q", errs, c.Hero.Title)
	}
	h, d := c.Hero, Default().Hero
	if h.Lead != d.Lead || !slices.Equal(h.Metrics, d.Metrics) || h.PrimaryCTALabel != d.PrimaryCTALabel ||
		h.ResumeCTALabel != d.ResumeCTALabel || !h.AvailabilityVisible || h.AvailabilityText != d.AvailabilityText {
		t.Errorf("partial hero lost fields: %+v", h)
	}
	c.Hero = d
	if !reflect.DeepEqual(c, base) {
		t.Error("sections left out changed")
	}

	c, _ = ApplySections(base, sections(t, `{"hero":{"metrics":[{"value":"1","label":"a"}]}}`))
	if !slices.Equal(c.Hero.Metrics, []Metric{{Value: "1", Label: "a"}}) || len(base.Hero.Metrics) != 3 {
		t.Errorf("metrics = %+v (base %d)", c.Hero.Metrics, len(base.Hero.Metrics))
	}

	c, _ = ApplySections(base, sections(t, `{"videos":{"featured":{"youtube_id":"gXT9izr0ljQ"}}}`))
	if errs := Normalize(&c); len(errs) != 1 || errs[0].String() != "videos.featured.title: required" {
		t.Errorf("featured not replaced whole: %v", errs)
	}

	leak := Default()
	leak.Organizations.Items[0].LogoURL = "/media/uploads/ust.png"
	leak.Organizations.Items[0].URL = "https://ust.com"
	c, _ = ApplySections(leak, sections(t, `{"organizations":{"items":[{"name":"Acme"}]}}`))
	if len(c.Organizations.Items) != 1 || c.Organizations.Items[0] != (Organization{Name: "Acme"}) {
		t.Errorf("leak: %+v", c.Organizations.Items)
	}
	if leak.Organizations.Items[0].Name != "UST" {
		t.Error("base modified")
	}

	c, errs = ApplySections(base, nil)
	if len(errs) > 0 || !reflect.DeepEqual(c, base) {
		t.Error("empty patch changed the document")
	}
}

func TestResetAndChanged(t *testing.T) {
	c := Default()
	c.Hero.Title = "X"
	c.About.Heading = "Y"
	r, err := Reset(c, []string{"hero"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Hero, Default().Hero) || r.About.Heading != "Y" || c.Hero.Title != "X" {
		t.Errorf("reset: %q %q %q", r.Hero.Title, r.About.Heading, c.Hero.Title)
	}
	if got := Changed(c, r); !slices.Equal(got, []string{"hero"}) {
		t.Errorf("Changed = %v", got)
	}
	if got := Changed(c, c); got == nil || len(got) != 0 {
		t.Errorf("Changed(c, c) = %#v", got)
	}
	n := Default()
	n.Identity.OtherLinks = nil
	if got := Changed(Default(), n); len(got) != 0 {
		t.Errorf("nil list counted as a change: %v", got)
	}
	var ve *ValidationError
	if _, err := Reset(c, []string{"hero", "nope"}); !errors.As(err, &ve) || ve.Error() != `unknown section "nope"` {
		t.Errorf("Reset unknown = %v", err)
	}
}

func TestExpand(t *testing.T) {
	c := Default()
	c.Identity.ExperienceYears = 12
	c.Identity.LinkedInURL = "https://x.example/{years}"
	c.Identity.OtherLinks = []Link{{Label: "{years} links", URL: "https://x.example/{years}"}}
	c.Organizations.Items[0].LogoURL = "/media/{years}.png"
	c.Videos.Featured.YouTubeID = "{years}"
	c.Identity.YouTubeHandle = "@{years}"
	c.About.Skills[0] = "{years} skills"
	e := Expand(c)
	if e.Hero.Metrics[0].Value != "12+ years" || !strings.Contains(e.Hero.Lead, "For 12+ years") || e.About.Skills[0] != "12 skills" ||
		e.Identity.OtherLinks[0].Label != "12 links" {
		t.Errorf("not expanded: %q %q %q", e.Hero.Metrics[0].Value, e.About.Skills[0], e.Identity.OtherLinks[0].Label)
	}
	if e.Identity.LinkedInURL != c.Identity.LinkedInURL || e.Identity.OtherLinks[0].URL != c.Identity.OtherLinks[0].URL ||
		e.Organizations.Items[0].LogoURL != c.Organizations.Items[0].LogoURL || e.Videos.Featured.YouTubeID != "{years}" ||
		e.Identity.YouTubeHandle != "@{years}" {
		t.Error("expanded a link, ID or handle")
	}
	if !strings.Contains(c.Hero.Lead, "{years}") || c.About.Skills[0] != "{years} skills" {
		t.Error("Expand modified its input")
	}
}

func TestDropMediaAndUsage(t *testing.T) {
	c := Default()
	c.Identity.PhotoURL = "/media/a.png"
	c.Organizations.Items[0].LogoURL = "/media/b.png"
	c.Organizations.Items[1].LogoURL = "/media/c.png"
	c.Organizations.Items[2].LogoURL = "/images/iit.png"
	c.Organizations.Items[3].LogoURL = "/media/b.png"
	if got := LocalMedia(c); !slices.Equal(got, []string{"/media/a.png", "/media/b.png", "/media/c.png"}) {
		t.Errorf("LocalMedia = %v", got)
	}
	if got := LocalMedia(Default()); got == nil || len(got) != 0 {
		t.Errorf("LocalMedia(Default()) = %#v", got)
	}
	if got := Usage(c, "/media/a.png"); !slices.Equal(got, []string{"Site profile › Photo"}) {
		t.Errorf("Usage photo = %v", got)
	}
	if got := Usage(c, "/media/b.png"); !slices.Equal(got, []string{"Home page › Organisations › UST logo", "Home page › Organisations › Ndedge Informatics logo"}) {
		t.Errorf("Usage logo = %v", got)
	}
	if got := Usage(c, "/media/zzz.png"); got == nil || len(got) != 0 {
		t.Errorf("Usage unused = %#v", got)
	}
	w := DropMedia(&c, map[string]bool{"/media/a.png": true, "/media/b.png": true})
	if c.Identity.PhotoURL != "/images/sayantan.jpg" || c.Organizations.Items[0].LogoURL != "" || c.Organizations.Items[1].LogoURL != "/media/c.png" ||
		c.Organizations.Items[3].LogoURL != "" {
		t.Errorf("DropMedia: %q %+v", c.Identity.PhotoURL, c.Organizations.Items)
	}
	want := []FieldError{
		{"identity.photo_url", "this image is no longer in the media library; the default photo is shown instead"},
		{"organizations.items[0].logo_url", "this image is no longer in the media library; the name is shown instead"},
		{"organizations.items[3].logo_url", "this image is no longer in the media library; the name is shown instead"},
	}
	if !slices.Equal(w, want) {
		t.Errorf("warnings = %v", w)
	}
	if w := DropMedia(&c, nil); w == nil || len(w) != 0 {
		t.Errorf("DropMedia(nil) = %#v", w)
	}
}

func TestResumeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"Sayantan Chakraborty": "Sayantan-Chakraborty-Resume.pdf",
		"":                     "Resume.pdf",
		"  ":                   "Resume.pdf",
		"Ünïcode  Name!":       "n-code-Name-Resume.pdf",
		"O'Brien, Jr.":         "O-Brien-Jr-Resume.pdf",
	} {
		if got := ResumeFilename(in); got != want {
			t.Errorf("ResumeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	e := &ValidationError{Errors: []FieldError{{"hero.title", "required"}, {"", "site content is too large"}}}
	if e.Error() != "hero.title: required; site content is too large" {
		t.Errorf("ValidationError = %q", e.Error())
	}
	if (&StaleError{}).Error() == "" {
		t.Error("empty StaleError message")
	}
	b, _ := json.Marshal(FieldError{"a", "b"})
	if string(b) != `{"path":"a","message":"b"}` {
		t.Errorf("FieldError json = %s", b)
	}
}
