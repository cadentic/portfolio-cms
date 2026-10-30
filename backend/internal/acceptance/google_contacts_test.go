package acceptance

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/campaigns"
	"github.com/cadentic/portfolio-cms/backend/internal/googlecontacts"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// fakeGoogle stands in for Google's OAuth and People API endpoints.
type fakeGoogle struct {
	mu        sync.Mutex
	challenge string
	revoked   []string
}

func (f *fakeGoogle) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	idToken := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"Owner@Gmail.com"}`)) + ".y"
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "gsecret" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			f.mu.Lock()
			ok := base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge && r.Form.Get("code") == "good-code"
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at1", "refresh_token": "refresh-secret-123",
				"scope": "openid " + googlecontacts.ScopeContacts + " https://www.googleapis.com/auth/userinfo.email", "id_token": idToken})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-secret-123" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at2"}`))
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer at2" {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/contactGroups", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contactGroups":[
			{"resourceName":"contactGroups/myContacts","formattedName":"My Contacts","groupType":"SYSTEM_CONTACT_GROUP","memberCount":9},
			{"resourceName":"contactGroups/abc","formattedName":"Newsletter","groupType":"USER_CONTACT_GROUP","memberCount":6}]}`))
	}))
	mux.HandleFunc("GET /v1/contactGroups/abc", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"memberCount":6,"memberResourceNames":["people/c1","people/c2","people/c3","people/c4","people/c5","people/c6"]}`))
	}))
	people := map[string]string{
		"people/c1": `{"resourceName":"people/c1","names":[{"displayName":"New Person","metadata":{"primary":true}}],
			"emailAddresses":[{"value":"work@example.com"},{"value":"New@Example.com","metadata":{"primary":true}}]}`,
		"people/c2": `{"resourceName":"people/c2","names":[{"displayName":"Fan"}],"emailAddresses":[{"value":"fan@example.com"}]}`,
		"people/c3": `{"resourceName":"people/c3","names":[{"displayName":"Gone"}],"emailAddresses":[{"value":"gone@example.com"}]}`,
		"people/c4": `{"resourceName":"people/c4","names":[{"displayName":"Phone only"}]}`,
		"people/c5": `{"resourceName":"people/c5","emailAddresses":[{"value":"not an email"}]}`,
		"people/c6": `{"resourceName":"people/c6","emailAddresses":[{"value":"new@example.com"}]}`,
	}
	mux.HandleFunc("GET /v1/people:batchGet", auth(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("personFields"), "emailAddresses") {
			t.Errorf("batchGet without emailAddresses: %s", r.URL.RawQuery)
		}
		var parts []string
		for _, rn := range r.URL.Query()["resourceNames"] {
			parts = append(parts, `{"person":`+people[rn]+`}`)
		}
		_, _ = w.Write([]byte(`{"responses":[` + strings.Join(parts, ",") + `]}`))
	}))
	return mux
}

func TestGoogleContactsSync(t *testing.T) {
	e := testenv.New(t)
	fake := &fakeGoogle{}
	g := httptest.NewServer(fake.handler(t))
	t.Cleanup(g.Close)
	gc := e.Server.GoogleContacts

	// Not configured: a clear message, no redirect.
	if r := e.Admin("POST", "/api/admin/google-contacts/connect", map[string]any{}); r.Status != 400 ||
		!strings.Contains(string(r.Body), "GOOGLE_OAUTH_CLIENT_ID") {
		t.Fatalf("connect unconfigured: %d %s", r.Status, r.Body)
	}
	gc.ClientID, gc.ClientSecret = "gclient", "gsecret"
	gc.AuthURL, gc.TokenURL, gc.RevokeURL, gc.PeopleURL = g.URL+"/auth", g.URL+"/token", g.URL+"/revoke", g.URL

	// Only the owner may start a connection.
	if r := e.Do("POST", "/api/admin/google-contacts/connect", map[string]any{}, nil); r.Status != 401 {
		t.Fatalf("anonymous connect: %d", r.Status)
	}
	var start struct{ URL string }
	if r := e.Admin("POST", "/api/admin/google-contacts/connect", map[string]any{}); r.Status != 200 || r.JSON(&start) != nil {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if q.Get("scope") != "openid email "+googlecontacts.ScopeContacts || q.Get("access_type") != "offline" ||
		q.Get("redirect_uri") != testenv.SiteURL+googlecontacts.CallbackPath || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("auth url: %s", start.URL)
	}
	fake.challenge = q.Get("code_challenge")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	callback := func(code, state string) string {
		resp, err := noFollow.Get(e.API.URL + googlecontacts.CallbackPath + "?code=" + code + "&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("callback status %d", resp.StatusCode)
		}
		return resp.Header.Get("Location")
	}
	if loc := callback("good-code", "forged-state"); !strings.Contains(loc, "error=") {
		t.Fatalf("forged state accepted: %s", loc)
	}
	if loc := callback("good-code", q.Get("state")); loc != testenv.SiteURL+"/admin/email/google-contacts?connected=1" {
		t.Fatalf("callback redirect: %s", loc)
	}
	if loc := callback("good-code", q.Get("state")); !strings.Contains(loc, "error=") {
		t.Fatalf("state reused: %s", loc)
	}
	if n := e.Count(`SELECT count(*) FROM google_contacts_sync WHERE position('refresh-secret-123'::bytea IN refresh_token_enc) > 0`); n != 0 {
		t.Fatal("refresh token stored in plain text")
	}

	var st googlecontacts.Status
	if r := e.Admin("GET", "/api/admin/google-contacts", nil); r.Status != 200 || r.JSON(&st) != nil || !st.Connected ||
		st.AccountEmail != "owner@gmail.com" || strings.Contains(string(r.Body), "refresh") {
		t.Fatalf("status: %d %s", r.Status, r.Body)
	}

	// Labels: only the account's own labels; choosing one needs the attestation.
	r := e.Admin("GET", "/api/admin/google-contacts/labels", nil)
	if r.Status != 200 || strings.Contains(string(r.Body), "myContacts") || !strings.Contains(string(r.Body), "contactGroups/abc") {
		t.Fatalf("labels: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("PUT", "/api/admin/google-contacts/label", map[string]any{"label_resource": "contactGroups/abc"}); r.Status != 400 {
		t.Fatalf("label without attestation: %d", r.Status)
	}
	if r := e.Admin("PUT", "/api/admin/google-contacts/label", map[string]any{"label_resource": "contactGroups/nope", "attest": true}); r.Status != 400 {
		t.Fatalf("unknown label: %d", r.Status)
	}
	if r := e.Admin("PUT", "/api/admin/google-contacts/label", map[string]any{"label_resource": "contactGroups/abc", "attest": true}); r.Status != 200 {
		t.Fatalf("label: %d %s", r.Status, r.Body)
	}

	// Existing people: a site subscriber, and someone who unsubscribed.
	e.Subscribe("fan@example.com")
	e.Subscribe("gone@example.com")
	gid := e.Str(`SELECT id::text FROM subscribers WHERE email='gone@example.com'`)
	if r := e.Admin("POST", "/api/admin/subscribers/"+gid+"/unsubscribe", nil); r.Status >= 300 {
		t.Fatalf("unsubscribe: %d", r.Status)
	}
	mailsBefore := len(e.Mock.Messages())

	var prev googlecontacts.Result
	if r := e.Admin("POST", "/api/admin/google-contacts/preview", map[string]any{}); r.Status != 200 || r.JSON(&prev) != nil {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	want := map[string]int{"total": 6, "new": 1, "subscribed": 1, "opted_out": 1, "no_email": 1, "invalid": 1, "duplicate": 1}
	for k, v := range want {
		if prev.Summary[k] != v {
			t.Fatalf("preview summary %v, want %v", prev.Summary, want)
		}
	}
	if n := e.Count(`SELECT count(*) FROM subscribers WHERE source='google_contacts'`); n != 0 {
		t.Fatal("preview wrote subscribers")
	}

	var res googlecontacts.Result
	if r := e.Admin("POST", "/api/admin/google-contacts/sync", map[string]any{}); r.Status != 200 || r.JSON(&res) != nil || res.Summary["added"] != 1 {
		t.Fatalf("sync: %d %s", r.Status, r.Body)
	}
	if got := e.Str(`SELECT status || ' ' || source || ' ' || name FROM subscribers WHERE email='new@example.com'`); got != "verified google_contacts New Person" {
		t.Fatalf("synced subscriber: %q", got)
	}
	if got := e.Str(`SELECT status || ' ' || source FROM subscribers WHERE email='gone@example.com'`); got != "unsubscribed website" {
		t.Fatalf("unsubscribed person changed: %q", got)
	}
	if n := e.Count(`SELECT count(*) FROM subscriber_consents c JOIN subscribers s ON s.id=c.subscriber_id
		WHERE s.email='new@example.com' AND c.action='attested' AND c.source='google_contacts'
		AND c.evidence->>'evidence' LIKE '%Newsletter%consent attested by owner' AND c.actor=$1`, testenv.AdminEmail); n != 1 {
		t.Fatal("no attested consent evidence")
	}
	e.Drain(3)
	if len(e.Mock.Messages()) != mailsBefore {
		t.Fatal("sync sent email")
	}
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='google_contacts.sync'`); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}

	// A separate list: the default newsletter audience leaves it out.
	aud, err := e.Campaigns.AudienceFor(e.Ctx, campaigns.Segment{})
	if err != nil || aud.Count != 1 {
		t.Fatalf("default audience: %+v %v", aud, err)
	}
	aud, err = e.Campaigns.AudienceFor(e.Ctx, campaigns.Segment{Sources: []string{googlecontacts.Source}})
	if err != nil || aud.Count != 1 || aud.Sample[0] == "fan@example.com" {
		t.Fatalf("google audience: %+v %v", aud, err)
	}

	// Again: nothing new; not due for the worker; paused stays paused.
	if r := e.Admin("POST", "/api/admin/google-contacts/sync", map[string]any{}); r.Status != 200 || !strings.Contains(string(r.Body), `"on_list":1`) {
		t.Fatalf("resync: %d %s", r.Status, r.Body)
	}
	if err := gc.RunDue(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='google_contacts.sync'`); n != 2 {
		t.Fatalf("worker synced before due: %d", n)
	}
	e.Exec(`UPDATE google_contacts_sync SET last_sync_at = now() - interval '7 hours'`)
	if r := e.Admin("POST", "/api/admin/google-contacts/pause", map[string]any{"paused": true}); r.Status != 204 {
		t.Fatalf("pause: %d", r.Status)
	}
	_ = gc.RunDue(e.Ctx)
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='google_contacts.sync'`); n != 2 {
		t.Fatal("paused sync ran")
	}
	_ = e.Admin("POST", "/api/admin/google-contacts/pause", map[string]any{"paused": false})
	if err := gc.RunDue(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='google_contacts.sync' AND actor='worker'`); n != 1 {
		t.Fatal("due sync did not run")
	}

	// Signing up on the site and confirming moves them to the regular list.
	e.Subscribe("new@example.com")
	if got := e.Str(`SELECT status || ' ' || source FROM subscribers WHERE email='new@example.com'`); got != "verified website" {
		t.Fatalf("after own sign-up: %q", got)
	}

	// Disconnect revokes the token at Google and forgets it.
	if r := e.Admin("POST", "/api/admin/google-contacts/disconnect", map[string]any{}); r.Status != 204 {
		t.Fatalf("disconnect: %d", r.Status)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != "refresh-secret-123" {
		t.Fatalf("revoked: %v", fake.revoked)
	}
	if n := e.Count(`SELECT count(*) FROM google_contacts_sync WHERE refresh_token_enc IS NULL AND label_resource IS NULL`); n != 1 {
		t.Fatal("disconnect kept the token")
	}
}

// With GOOGLE_CONTACTS_ENABLED off, only the status call answers (so the
// admin can hide the tab); every other endpoint, and the callback, is absent.
func TestGoogleContactsDisabled(t *testing.T) {
	e := testenv.New(t)
	e.Server.Cfg.GoogleContactsEnabled = false
	off := httptest.NewServer(e.Server.Routes())
	t.Cleanup(off.Close)
	e.API = off

	if r := e.Admin("GET", "/api/admin/google-contacts", nil); r.Status != 200 || string(r.Body) != "{\"enabled\":false}\n" {
		t.Fatalf("status when off: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"connect", "sync", "preview", "disconnect"} {
		if r := e.Admin("POST", "/api/admin/google-contacts/"+p, map[string]any{}); r.Status != 404 {
			t.Fatalf("%s when off: %d", p, r.Status)
		}
	}
	if r := e.Do("GET", googlecontacts.CallbackPath+"?code=x&state=y", nil, nil); r.Status == http.StatusSeeOther {
		t.Fatal("callback answered when off")
	}
	if r := e.Admin("GET", "/api/admin/google-contacts", nil); strings.Contains(string(r.Body), "configured") {
		t.Fatal("status leaked details when off")
	}
}
