// Package sitecontent is the owner-editable copy of the public site (profile,
// home page sections, videos, SEO text) stored as one JSON document in
// app_settings under Key. A missing row means Default(). The package owns the
// types, defaults, validation, merge, {years} expansion and a short cache.
package sitecontent

import (
	"fmt"
	"strings"
	"time"
)

const Key = "site_content"

// Sections lists the top-level keys of Content, in field order.
var Sections = []string{"identity", "navigation", "hero", "about", "experience", "work", "organizations",
	"expertise", "videos", "blog", "newsletter", "faq", "contact", "seo"}

type Content struct {
	Identity      Identity      `json:"identity"`
	Navigation    Navigation    `json:"navigation"`
	Hero          Hero          `json:"hero"`
	About         About         `json:"about"`
	Experience    Experience    `json:"experience"`
	Work          Work          `json:"work"`
	Organizations Organizations `json:"organizations"`
	Expertise     Expertise     `json:"expertise"`
	Videos        Videos        `json:"videos"`
	Blog          Blog          `json:"blog"`
	Newsletter    Newsletter    `json:"newsletter"`
	FAQ           FAQ           `json:"faq"`
	Contact       Contact       `json:"contact"`
	SEO           SEO           `json:"seo"`
}

type Identity struct {
	Name            string      `json:"name"`
	Initials        string      `json:"initials"`
	ShortRole       string      `json:"short_role"`
	Location        string      `json:"location"`
	City            string      `json:"city"`
	CountryCode     string      `json:"country_code"`
	ExperienceYears int         `json:"experience_years"`
	PhotoURL        string      `json:"photo_url"`
	Summary         string      `json:"summary"`
	LinkedInURL     string      `json:"linkedin_url"`
	GitHubURL       string      `json:"github_url"`
	YouTubeURL      string      `json:"youtube_url"`
	YouTubeHandle   string      `json:"youtube_handle"`
	OtherLinks      []Link      `json:"other_links"`
	Education       []Education `json:"education"`
	Languages       []string    `json:"languages"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Education struct {
	Degree string `json:"degree"`
	School string `json:"school"`
	Years  string `json:"years"`
}

type NavCard struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type Navigation struct {
	BrandSubtitle string  `json:"brand_subtitle"`
	ResumeLabel   string  `json:"resume_label"`
	MegaWork      NavCard `json:"mega_work"`
	MegaVideos    NavCard `json:"mega_videos"`
	MegaCareer    NavCard `json:"mega_career"`
	MegaBlog      NavCard `json:"mega_blog"`
}

type Metric struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Hero struct {
	Eyebrow             string   `json:"eyebrow"`
	Title               string   `json:"title"`
	TitleHighlight      string   `json:"title_highlight"`
	Lead                string   `json:"lead"`
	PrimaryCTALabel     string   `json:"primary_cta_label"`
	ResumeCTALabel      string   `json:"resume_cta_label"`
	AvailabilityVisible bool     `json:"availability_visible"`
	AvailabilityText    string   `json:"availability_text"`
	Metrics             []Metric `json:"metrics"`
}

type About struct {
	Visible     bool     `json:"visible"`
	Label       string   `json:"label"`
	StampKicker string   `json:"stamp_kicker"`
	StampLines  []string `json:"stamp_lines"`
	StampText   string   `json:"stamp_text"`
	Kicker      string   `json:"kicker"`
	Heading     string   `json:"heading"`
	ShowSummary bool     `json:"show_summary"`
	Paragraphs  []string `json:"paragraphs"`
	Skills      []string `json:"skills"`
}

type Role struct {
	Period string   `json:"period"`
	Title  string   `json:"title"`
	Org    string   `json:"org"`
	Points []string `json:"points"`
}

type EarlierRole struct {
	Period string `json:"period"`
	Title  string `json:"title"`
	Org    string `json:"org"`
}

type Experience struct {
	Visible      bool          `json:"visible"`
	Label        string        `json:"label"`
	Heading      string        `json:"heading"`
	Intro        string        `json:"intro"`
	CTALabel     string        `json:"cta_label"`
	Roles        []Role        `json:"roles"`
	EarlierLabel string        `json:"earlier_label"`
	EarlierRoles []EarlierRole `json:"earlier_roles"`
}

type WorkItem struct {
	Tag   string `json:"tag"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type Work struct {
	Visible bool       `json:"visible"`
	Label   string     `json:"label"`
	Heading string     `json:"heading"`
	Intro   string     `json:"intro"`
	Items   []WorkItem `json:"items"`
}

type Organization struct {
	Name    string `json:"name"`
	Note    string `json:"note"`
	LogoURL string `json:"logo_url"`
	URL     string `json:"url"`
}

type Organizations struct {
	Visible bool           `json:"visible"`
	Label   string         `json:"label"`
	Heading string         `json:"heading"`
	Intro   string         `json:"intro"`
	Items   []Organization `json:"items"`
}

type ExpertiseItem struct {
	Title string   `json:"title"`
	Text  string   `json:"text"`
	Chips []string `json:"chips"`
}

type Expertise struct {
	Visible bool            `json:"visible"`
	Label   string          `json:"label"`
	Heading string          `json:"heading"`
	Intro   string          `json:"intro"`
	Items   []ExpertiseItem `json:"items"`
}

type Video struct {
	YouTubeID   string `json:"youtube_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	PublishedOn string `json:"published_on"`
}

type Videos struct {
	Visible              bool    `json:"visible"`
	Label                string  `json:"label"`
	Heading              string  `json:"heading"`
	Intro                string  `json:"intro"`
	ChannelCTALabel      string  `json:"channel_cta_label"`
	Featured             Video   `json:"featured"`
	Items                []Video `json:"items"`
	IncludeArticleVideos bool    `json:"include_article_videos"`
	PageHeading          string  `json:"page_heading"`
	PageIntro            string  `json:"page_intro"`
	PageCTALabel         string  `json:"page_cta_label"`
	EmptyText            string  `json:"empty_text"`
	PlaceholderTitle     string  `json:"placeholder_title"`
	PlaceholderText      string  `json:"placeholder_text"`
}

type Blog struct {
	Visible     bool   `json:"visible"`
	Label       string `json:"label"`
	Heading     string `json:"heading"`
	Intro       string `json:"intro"`
	CTALabel    string `json:"cta_label"`
	PageHeading string `json:"page_heading"`
	PageIntro   string `json:"page_intro"`
	EmptyText   string `json:"empty_text"`
}

type Newsletter struct {
	Visible bool   `json:"visible"`
	Label   string `json:"label"`
	Heading string `json:"heading"`
	Intro   string `json:"intro"`
}

type QA struct {
	Q string `json:"q"`
	A string `json:"a"`
}

type FAQ struct {
	Visible bool   `json:"visible"`
	Label   string `json:"label"`
	Heading string `json:"heading"`
	Items   []QA   `json:"items"`
}

type Contact struct {
	Label     string `json:"label"`
	Heading   string `json:"heading"`
	Intro     string `json:"intro"`
	ShowLinks bool   `json:"show_links"`
}

type SEO struct {
	HomeTitle         string `json:"home_title"`
	HomeDescription   string `json:"home_description"`
	VideosTitle       string `json:"videos_title"`
	VideosDescription string `json:"videos_description"`
	BlogTitle         string `json:"blog_title"`
	BlogDescription   string `json:"blog_description"`
	LLMsSummary       string `json:"llms_summary"`
}

// FieldError is one problem with one field. Path uses dots and zero-based
// indices, e.g. "organizations.items[2].logo_url"; "" means the whole document.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e FieldError) String() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// ValidationError is returned for content the owner has to fix (400).
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		lines[i] = fe.String()
	}
	return strings.Join(lines, "; ")
}

// StaleError is returned when base_updated_at no longer matches the stored
// document (409). Both fields are nil when there is no stored row.
type StaleError struct {
	UpdatedAt *time.Time
	UpdatedBy *string
}

func (e *StaleError) Error() string {
	if e.UpdatedAt == nil {
		return "site content was changed elsewhere (it has no saved version)"
	}
	by := "unknown"
	if e.UpdatedBy != nil && *e.UpdatedBy != "" {
		by = *e.UpdatedBy
	}
	return fmt.Sprintf("site content was changed elsewhere (by %s at %s)", by, e.UpdatedAt.UTC().Format(time.RFC3339))
}
