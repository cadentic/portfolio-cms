// Command mockprovider imitates the parts of the Brevo and Mailjet APIs the
// backend uses, for local development and failover drills. Nothing is ever
// delivered: messages are kept in memory and shown at http://localhost:8025.
//
// Each provider has a switchable mode so you can watch the queue fail over:
//
//	ok        accept the message (default)
//	error500  reject with HTTP 500 (transient: retry, then failover)
//	ratelimit reject with HTTP 429 and Retry-After
//	auth      reject with HTTP 401 (provider-level failure: immediate failover)
//	timeout   hold the request for 60s (the client gives up: acceptance unknown)
//	unknown   store the message, then answer 504 (accepted but we can't tell)
//
// Switch with the UI or: curl -XPOST 'localhost:8025/_mock/mode?provider=brevo&mode=error500'
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type message struct {
	Seq        int               `json:"seq"`
	Provider   string            `json:"provider"`
	MessageID  string            `json:"message_id"`
	InternalID string            `json:"internal_id"`
	To         string            `json:"to"`
	From       string            `json:"from"`
	Subject    string            `json:"subject"`
	HTML       string            `json:"html"`
	Headers    map[string]string `json:"headers"`
	ReceivedAt time.Time         `json:"received_at"`
	Duplicate  bool              `json:"duplicate"`
}

type state struct {
	mu       sync.Mutex
	seq      int
	messages []*message
	modes    map[string]string
	idem     map[string]string // brevo idempotencyKey -> messageId
	seen     map[string]int    // internal id -> deliveries across providers
	webhooks map[string]string // provider -> webhook URL (with credentials)
}

var st = &state{
	modes:    map[string]string{"brevo": "ok", "mailjet": "ok"},
	idem:     map[string]string{},
	seen:     map[string]int{},
	webhooks: map[string]string{"brevo": os.Getenv("MOCK_WEBHOOK_BREVO_URL"), "mailjet": os.Getenv("MOCK_WEBHOOK_MAILJET_URL")},
}

var validModes = map[string]bool{"ok": true, "error500": true, "ratelimit": true, "auth": true, "timeout": true, "unknown": true}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// gate applies the provider's failure mode. It returns true when the request
// should proceed to store the message; storeThen504 is set for "unknown".
func gate(w http.ResponseWriter, r *http.Request, provider string) (proceed, storeThen504 bool) {
	st.mu.Lock()
	mode := st.modes[provider]
	st.mu.Unlock()
	switch mode {
	case "error500":
		http.Error(w, `{"code":"internal_error","message":"mock outage"}`, http.StatusInternalServerError)
	case "ratelimit":
		w.Header().Set("Retry-After", "5")
		http.Error(w, `{"code":"too_many_requests","message":"mock rate limit"}`, http.StatusTooManyRequests)
	case "auth":
		http.Error(w, `{"code":"unauthorized","message":"Key not found"}`, http.StatusUnauthorized)
	case "timeout":
		select {
		case <-time.After(60 * time.Second):
		case <-r.Context().Done():
		}
	case "unknown":
		return true, true
	default:
		return true, false
	}
	return false, false
}

func record(m *message) *message {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.seq++
	m.Seq = st.seq
	m.ReceivedAt = time.Now().UTC()
	if m.InternalID != "" {
		st.seen[m.InternalID]++
		m.Duplicate = st.seen[m.InternalID] > 1
		if m.Duplicate {
			log.Printf("DUPLICATE delivery for internal id %s (%d copies)", m.InternalID, st.seen[m.InternalID])
		}
	}
	st.messages = append(st.messages, m)
	return m
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// --- Brevo ---------------------------------------------------------------

func brevoSend(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("api-key") == "" {
		http.Error(w, `{"code":"unauthorized","message":"Key not found"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Sender      struct{ Email string }   `json:"sender"`
		To          []struct{ Email string } `json:"to"`
		Subject     string                   `json:"subject"`
		HTMLContent string                   `json:"htmlContent"`
		Headers     map[string]string        `json:"headers"`
		Tags        []string                 `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.To) == 0 {
		http.Error(w, `{"code":"invalid_parameter","message":"bad body"}`, http.StatusBadRequest)
		return
	}
	proceed, unknown := gate(w, r, "brevo")
	if !proceed {
		return
	}
	key := req.Headers["idempotencyKey"]
	st.mu.Lock()
	existing, dup := st.idem[key]
	st.mu.Unlock()
	if key != "" && dup {
		// Same behaviour as Brevo: a repeated idempotency key returns the original id.
		writeJSON(w, http.StatusCreated, map[string]string{"messageId": existing})
		return
	}
	id := "<" + randID() + "@smtp-relay.mock>"
	if key != "" {
		st.mu.Lock()
		st.idem[key] = id
		st.mu.Unlock()
	}
	record(&message{Provider: "brevo", MessageID: id, InternalID: req.Headers["X-Mailin-custom"], To: req.To[0].Email,
		From: req.Sender.Email, Subject: req.Subject, HTML: req.HTMLContent, Headers: req.Headers})
	if unknown {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"messageId": id})
}

func brevoEvents(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimPrefix(r.URL.Query().Get("tags"), "job:")
	var events []map[string]string
	st.mu.Lock()
	for _, m := range st.messages {
		if m.Provider == "brevo" && m.InternalID == tag {
			events = append(events, map[string]string{"messageId": m.MessageID, "event": "requests", "tag": "job:" + tag})
		}
	}
	st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"events": events})
}

// --- Mailjet -------------------------------------------------------------

func mailjetSend(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u == "" || p == "" {
		http.Error(w, `{"ErrorMessage":"API key authentication/authorization failure"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Messages []struct {
			From     struct{ Email string }
			To       []struct{ Email string }
			Subject  string
			HTMLPart string
			CustomID string
			Headers  map[string]string
		}
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 || len(req.Messages[0].To) == 0 {
		http.Error(w, `{"ErrorMessage":"bad body"}`, http.StatusBadRequest)
		return
	}
	proceed, unknown := gate(w, r, "mailjet")
	if !proceed {
		return
	}
	m := req.Messages[0]
	id := fmt.Sprintf("%d", time.Now().UnixNano()/1000)
	record(&message{Provider: "mailjet", MessageID: id, InternalID: m.CustomID, To: m.To[0].Email,
		From: m.From.Email, Subject: m.Subject, HTML: m.HTMLPart, Headers: m.Headers})
	if unknown {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, 200, map[string]any{"Messages": []map[string]any{{
		"Status": "success", "CustomID": m.CustomID,
		"To": []map[string]any{{"Email": m.To[0].Email, "MessageUUID": randID(), "MessageID": json.Number(id)}},
	}}})
}

func mailjetMessages(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("CustomID")
	var data []map[string]any
	st.mu.Lock()
	for _, m := range st.messages {
		if m.Provider == "mailjet" && m.InternalID == cid {
			data = append(data, map[string]any{"ID": json.Number(m.MessageID), "CustomID": cid})
		}
	}
	st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"Count": len(data), "Data": data})
}

// --- control + webhooks --------------------------------------------------

func setMode(w http.ResponseWriter, r *http.Request) {
	p, mode := r.FormValue("provider"), r.FormValue("mode")
	if (p != "brevo" && p != "mailjet") || !validModes[mode] {
		http.Error(w, "provider must be brevo|mailjet and mode one of ok,error500,ratelimit,auth,timeout,unknown", 400)
		return
	}
	st.mu.Lock()
	st.modes[p] = mode
	st.mu.Unlock()
	log.Printf("mode %s -> %s", p, mode)
	back(w, r)
}

func setWebhook(w http.ResponseWriter, r *http.Request) {
	p := r.FormValue("provider")
	if p != "brevo" && p != "mailjet" {
		http.Error(w, "bad provider", 400)
		return
	}
	st.mu.Lock()
	st.webhooks[p] = strings.TrimSpace(r.FormValue("url"))
	st.mu.Unlock()
	back(w, r)
}

// fireEvent sends a webhook for a stored message in the provider's own format.
func fireEvent(w http.ResponseWriter, r *http.Request) {
	seq := r.FormValue("seq")
	ev := r.FormValue("event") // delivered | hard_bounce | spam | unsubscribed | opened
	var m *message
	st.mu.Lock()
	for _, x := range st.messages {
		if fmt.Sprint(x.Seq) == seq {
			m = x
		}
	}
	var target string
	if m != nil {
		target = st.webhooks[m.Provider]
	}
	st.mu.Unlock()
	if m == nil || target == "" {
		http.Error(w, "unknown message or webhook URL not configured for this provider", 400)
		return
	}
	var body any
	now := time.Now()
	if m.Provider == "brevo" {
		body = map[string]any{"event": ev, "email": m.To, "message-id": m.MessageID, "ts_epoch": now.UnixMilli(),
			"ts_event": now.Unix(), "X-Mailin-custom": m.InternalID, "tags": []string{"job:" + m.InternalID}, "subject": m.Subject}
	} else {
		mj := map[string]string{"delivered": "sent", "hard_bounce": "bounce", "spam": "spam", "unsubscribed": "unsub", "opened": "open"}[ev]
		body = map[string]any{"event": mj, "time": now.Unix(), "MessageID": json.Number(m.MessageID), "Message_GUID": randID(),
			"email": m.To, "CustomID": m.InternalID, "hard_bounce": ev == "hard_bounce"}
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(target, "application/json", bytes.NewReader(b))
	if err != nil {
		http.Error(w, "webhook failed: "+err.Error(), 502)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	log.Printf("webhook %s %s -> HTTP %d", m.Provider, ev, resp.StatusCode)
	back(w, r)
}

func back(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func listJSON(w http.ResponseWriter, _ *http.Request) {
	st.mu.Lock()
	defer st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"modes": st.modes, "messages": st.messages, "deliveries_per_internal_id": st.seen})
}

func reset(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	st.messages, st.idem, st.seen = nil, map[string]string{}, map[string]int{}
	st.modes = map[string]string{"brevo": "ok", "mailjet": "ok"}
	st.mu.Unlock()
	back(w, r)
}

var page = template.Must(template.New("p").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Mock email providers</title>
<meta http-equiv="refresh" content="5"><style>body{font:14px system-ui;margin:24px;background:#0b1020;color:#e8ecf5}
table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #263050;padding:6px;text-align:left;vertical-align:top}
.dup{color:#ff8080;font-weight:700}form{display:inline}button,select,input{background:#18213d;color:#e8ecf5;border:1px solid #3a4670;border-radius:6px;padding:4px 8px}
.card{background:#121a33;padding:12px 16px;border-radius:10px;margin-bottom:16px}</style></head><body>
<h1>Mock email providers</h1><p>Nothing here is delivered. Page refreshes every 5s.</p>
{{range $p, $m := .Modes}}<div class="card"><strong>{{$p}}</strong> mode: <b>{{$m}}</b>
<form method="post" action="/_mock/mode"><input type="hidden" name="provider" value="{{$p}}"><select name="mode">
<option>ok</option><option>error500</option><option>ratelimit</option><option>auth</option><option>timeout</option><option>unknown</option></select><button>Set</button></form>
<form method="post" action="/_mock/webhook"><input type="hidden" name="provider" value="{{$p}}"><input name="url" size="70" placeholder="http://api:8080/api/webhooks/&lt;provider-id&gt; (with user:secret@)" value="{{index $.Webhooks $p}}"><button>Save webhook URL</button></form></div>{{end}}
<form method="post" action="/_mock/reset"><button>Reset everything</button></form>
<h2>Received messages ({{len .Messages}})</h2><table><tr><th>#</th><th>Provider</th><th>To</th><th>Subject</th><th>Internal id</th><th>Received</th><th>Webhook</th></tr>
{{range .Messages}}<tr><td>{{.Seq}}</td><td>{{.Provider}}</td><td>{{.To}}</td><td>{{.Subject}}</td><td>{{.InternalID}}{{if .Duplicate}} <span class="dup">DUPLICATE</span>{{end}}</td><td>{{.ReceivedAt.Format "15:04:05"}}</td>
<td><form method="post" action="/_mock/event"><input type="hidden" name="seq" value="{{.Seq}}"><select name="event"><option>delivered</option><option>hard_bounce</option><option>spam</option><option>unsubscribed</option><option>opened</option></select><button>Send</button></form></td></tr>{{end}}
</table></body></html>`))

func ui(w http.ResponseWriter, _ *http.Request) {
	st.mu.Lock()
	msgs := append([]*message(nil), st.messages...)
	modes := map[string]string{}
	hooks := map[string]string{}
	for k, v := range st.modes {
		modes[k] = v
	}
	for k, v := range st.webhooks {
		hooks[k] = v
	}
	st.mu.Unlock()
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Seq > msgs[j].Seq })
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]any{"Modes": modes, "Webhooks": hooks, "Messages": msgs})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v3/smtp/email", brevoSend)
	mux.HandleFunc("GET /v3/account", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"email": "mock@brevo", "plan": []map[string]any{{"type": "free", "credits": 300, "creditsType": "sendLimit"}}})
	})
	mux.HandleFunc("GET /v3/smtp/statistics/events", brevoEvents)
	mux.HandleFunc("POST /v3.1/send", mailjetSend)
	mux.HandleFunc("GET /v3/REST/user", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"Count": 1}) })
	mux.HandleFunc("GET /v3/REST/message", mailjetMessages)
	mux.HandleFunc("POST /_mock/mode", setMode)
	mux.HandleFunc("POST /_mock/webhook", setWebhook)
	mux.HandleFunc("POST /_mock/event", fireEvent)
	mux.HandleFunc("POST /_mock/reset", reset)
	mux.HandleFunc("GET /_mock/messages", listJSON)
	mux.HandleFunc("GET /{$}", ui)
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = ":8025"
	}
	log.Printf("mock email providers on %s", addr)
	log.Fatal((&http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
}
