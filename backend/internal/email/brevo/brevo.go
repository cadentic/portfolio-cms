// Package brevo implements the Brevo (formerly Sendinblue) transactional email adapter.
//
// API reference: https://developers.brevo.com/reference/sendtransacemail
package brevo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
)

const defaultBaseURL = "https://api.brevo.com"

type Adapter struct{ Client *http.Client }

func New(client *http.Client) *Adapter { return &Adapter{Client: client} }

func (a *Adapter) Kind() string                     { return "brevo" }
func (a *Adapter) RequiredCredentials() []string    { return []string{"api_key"} }
func (a *Adapter) IdempotencyWindow() time.Duration { return 15 * time.Minute } // Brevo idempotencyKey TTL

func base(cfg email.ProviderConfig) string {
	if cfg.BaseURL != "" {
		return strings.TrimRight(cfg.BaseURL, "/")
	}
	return defaultBaseURL
}

func (a *Adapter) headers(cfg email.ProviderConfig) func(http.Header) {
	return func(h http.Header) { h.Set("api-key", cfg.Credentials["api_key"]) }
}

type addr struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type sendRequest struct {
	Sender      addr              `json:"sender"`
	To          []addr            `json:"to"`
	ReplyTo     *addr             `json:"replyTo,omitempty"`
	Subject     string            `json:"subject"`
	HTMLContent string            `json:"htmlContent"`
	TextContent string            `json:"textContent,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
}

func (a *Adapter) Send(ctx context.Context, cfg email.ProviderConfig, msg email.Message) email.SendResult {
	headers := map[string]string{}
	for k, v := range msg.Headers {
		headers[k] = v
	}
	// Brevo deduplicates requests carrying the same idempotencyKey for 15 minutes.
	headers["idempotencyKey"] = msg.InternalID
	// Echoed back in webhooks as X-Mailin-custom, letting us correlate events.
	headers["X-Mailin-custom"] = msg.InternalID
	req := sendRequest{
		Sender:      addr{msg.From.Email, msg.From.Name},
		To:          []addr{{msg.To.Email, msg.To.Name}},
		Subject:     msg.Subject,
		HTMLContent: msg.HTML,
		TextContent: msg.Text,
		Headers:     headers,
		Tags:        append([]string{"job:" + msg.InternalID, string(msg.Category)}, msg.Tags...),
	}
	if msg.ReplyTo != nil && msg.ReplyTo.Email != "" {
		req.ReplyTo = &addr{msg.ReplyTo.Email, msg.ReplyTo.Name}
	}
	if sandbox, _ := cfg.Settings["sandbox"].(bool); sandbox {
		// Brevo validates the request but drops the email.
		req.Headers["X-Sib-Sandbox"] = "drop"
	}
	raw := email.DoJSON(ctx, a.Client, http.MethodPost, base(cfg)+"/v3/smtp/email", req, a.headers(cfg))
	res := email.ClassifyHTTP(raw)
	if res.Outcome == email.OutcomeAccepted {
		var body struct {
			MessageID  string   `json:"messageId"`
			MessageIDs []string `json:"messageIds"`
		}
		_ = json.Unmarshal(raw.Body, &body)
		res.ProviderMessageID = body.MessageID
		if res.ProviderMessageID == "" && len(body.MessageIDs) > 0 {
			res.ProviderMessageID = body.MessageIDs[0]
		}
		return res
	}
	var eb struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw.Body, &eb) == nil && eb.Code != "" {
		res.ErrorCode = eb.Code
		if eb.Code == "not_enough_credits" {
			res.QuotaExhausted = true
		}
	}
	return res
}

func (a *Adapter) HealthCheck(ctx context.Context, cfg email.ProviderConfig) email.HealthResult {
	if cfg.Credentials["api_key"] == "" {
		return email.HealthResult{OK: false, Message: "API key secret is not configured"}
	}
	raw := email.DoJSON(ctx, a.Client, http.MethodGet, base(cfg)+"/v3/account", nil, a.headers(cfg))
	if raw.Status != http.StatusOK {
		msg := fmt.Sprintf("account check failed (HTTP %d)", raw.Status)
		if raw.Err != nil {
			msg = "account check failed: " + email.RedactString(raw.Err.Error())
		}
		return email.HealthResult{OK: false, Message: msg}
	}
	var acct struct {
		Plan []struct {
			Type        string  `json:"type"`
			Credits     float64 `json:"credits"`
			CreditsType string  `json:"creditsType"`
		} `json:"plan"`
	}
	_ = json.Unmarshal(raw.Body, &acct)
	quota := map[string]any{}
	for _, p := range acct.Plan {
		quota[p.Type] = map[string]any{"credits": p.Credits, "credits_type": p.CreditsType}
	}
	return email.HealthResult{OK: true, Message: "account reachable", Quota: quota}
}

// Lookup searches Brevo's transactional event log for the job tag we attach to
// every message. A "requests" event means Brevo accepted it.
func (a *Adapter) Lookup(ctx context.Context, cfg email.ProviderConfig, internalID, toEmail string, since time.Time) (email.LookupResult, error) {
	q := url.Values{}
	q.Set("email", toEmail)
	q.Set("tags", "job:"+internalID)
	q.Set("startDate", since.UTC().Add(-24*time.Hour).Format("2006-01-02"))
	q.Set("endDate", time.Now().UTC().Format("2006-01-02"))
	q.Set("limit", "50")
	raw := email.DoJSON(ctx, a.Client, http.MethodGet, base(cfg)+"/v3/smtp/statistics/events?"+q.Encode(), nil, a.headers(cfg))
	if raw.Err != nil || raw.Status != http.StatusOK {
		return email.LookupResult{}, fmt.Errorf("brevo lookup failed: status %d", raw.Status)
	}
	var body struct {
		Events []struct {
			MessageID string `json:"messageId"`
			Event     string `json:"event"`
			Tag       string `json:"tag"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw.Body, &body); err != nil {
		return email.LookupResult{}, err
	}
	for _, e := range body.Events {
		if e.MessageID != "" {
			return email.LookupResult{Status: email.LookupFound, ProviderMessageID: e.MessageID}, nil
		}
	}
	return email.LookupResult{Status: email.LookupNotFound}, nil
}

func (a *Adapter) VerifyWebhook(r *http.Request, cfg email.ProviderConfig) error {
	return email.VerifySharedSecret(r, cfg.WebhookSecret)
}

type webhookEvent struct {
	Event     string          `json:"event"`
	Email     string          `json:"email"`
	ID        json.Number     `json:"id"`
	MessageID string          `json:"message-id"`
	TsEvent   int64           `json:"ts_event"`
	TsEpoch   int64           `json:"ts_epoch"`
	Date      string          `json:"date"`
	Tags      []string        `json:"tags"`
	Tag       json.RawMessage `json:"tag"`
	Custom    string          `json:"X-Mailin-custom"`
	Reason    string          `json:"reason"`
	Link      string          `json:"link"`
	Subject   string          `json:"subject"`
	SendingIP string          `json:"sending_ip"`
}

var eventMap = map[string]email.EventType{
	"request":       email.EventAccepted,
	"sent":          email.EventAccepted,
	"delivered":     email.EventDelivered,
	"deferred":      email.EventDeferred,
	"soft_bounce":   email.EventSoftBounce,
	"hard_bounce":   email.EventHardBounce,
	"spam":          email.EventComplaint,
	"complaint":     email.EventComplaint,
	"unsubscribed":  email.EventUnsubscribed,
	"opened":        email.EventOpened,
	"unique_opened": email.EventOpened,
	"proxy_open":    email.EventOpened,
	"click":         email.EventClicked,
	"blocked":       email.EventBlocked,
	"invalid_email": email.EventInvalid,
	"error":         email.EventError,
}

// ParseWebhook accepts a single event object or an array (batched webhooks).
func (a *Adapter) ParseWebhook(body []byte) ([]email.Event, error) {
	var list []webhookEvent
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, err
		}
	} else {
		var one webhookEvent
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, err
		}
		list = []webhookEvent{one}
	}
	var rawList []map[string]any
	if strings.HasPrefix(trimmed, "[") {
		_ = json.Unmarshal(body, &rawList)
	} else {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		rawList = []map[string]any{m}
	}
	out := make([]email.Event, 0, len(list))
	for i, e := range list {
		t, ok := eventMap[e.Event]
		if !ok {
			t = email.EventUnknown
		}
		occurred := time.Now().UTC()
		switch {
		case e.TsEpoch > 0:
			occurred = time.UnixMilli(e.TsEpoch).UTC()
		case e.TsEvent > 0:
			occurred = time.Unix(e.TsEvent, 0).UTC()
		}
		internal := e.Custom
		if internal == "" {
			for _, tag := range e.Tags {
				if strings.HasPrefix(tag, "job:") {
					internal = strings.TrimPrefix(tag, "job:")
				}
			}
		}
		// Brevo has no unique event ID; derive a stable key from the fields
		// that identify one occurrence.
		h := sha256.Sum256([]byte(e.MessageID + "|" + e.Event + "|" + strconv.FormatInt(e.TsEpoch, 10) + "|" +
			strconv.FormatInt(e.TsEvent, 10) + "|" + e.Link + "|" + strings.ToLower(e.Email)))
		var payload map[string]any
		if i < len(rawList) {
			payload = email.RedactMap(rawList[i])
		}
		out = append(out, email.Event{
			DedupeKey:         hex.EncodeToString(h[:]),
			ProviderMessageID: e.MessageID,
			InternalID:        internal,
			Email:             e.Email,
			Type:              t,
			RawType:           e.Event,
			OccurredAt:        occurred,
			Payload:           payload,
		})
	}
	return out, nil
}
