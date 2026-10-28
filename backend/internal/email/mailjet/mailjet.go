// Package mailjet implements the Mailjet Send API v3.1 adapter.
//
// API reference: https://dev.mailjet.com/email/reference/send-emails/
package mailjet

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

const defaultBaseURL = "https://api.mailjet.com"

type Adapter struct{ Client *http.Client }

func New(client *http.Client) *Adapter { return &Adapter{Client: client} }

func (a *Adapter) Kind() string                  { return "mailjet" }
func (a *Adapter) RequiredCredentials() []string { return []string{"api_key", "secret_key"} }

// Mailjet has no request-level idempotency; duplicate protection relies on
// CustomID lookups before any resend.
func (a *Adapter) IdempotencyWindow() time.Duration { return 0 }

func base(cfg email.ProviderConfig) string {
	if cfg.BaseURL != "" {
		return strings.TrimRight(cfg.BaseURL, "/")
	}
	return defaultBaseURL
}

func (a *Adapter) auth(cfg email.ProviderConfig) func(http.Header) {
	return func(h http.Header) {
		r := &http.Request{Header: h}
		r.SetBasicAuth(cfg.Credentials["api_key"], cfg.Credentials["secret_key"])
	}
}

type mjAddr struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type mjMessage struct {
	From     mjAddr            `json:"From"`
	To       []mjAddr          `json:"To"`
	ReplyTo  *mjAddr           `json:"ReplyTo,omitempty"`
	Subject  string            `json:"Subject"`
	TextPart string            `json:"TextPart,omitempty"`
	HTMLPart string            `json:"HTMLPart"`
	CustomID string            `json:"CustomID"`
	Headers  map[string]string `json:"Headers,omitempty"`
}

type sendRequest struct {
	Messages    []mjMessage `json:"Messages"`
	SandboxMode bool        `json:"SandboxMode,omitempty"`
}

type sendResponse struct {
	Messages []struct {
		Status string `json:"Status"`
		To     []struct {
			Email       string      `json:"Email"`
			MessageUUID string      `json:"MessageUUID"`
			MessageID   json.Number `json:"MessageID"`
		} `json:"To"`
		Errors []struct {
			ErrorCode    string `json:"ErrorCode"`
			StatusCode   int    `json:"StatusCode"`
			ErrorMessage string `json:"ErrorMessage"`
		} `json:"Errors"`
	} `json:"Messages"`
}

func (a *Adapter) Send(ctx context.Context, cfg email.ProviderConfig, msg email.Message) email.SendResult {
	m := mjMessage{
		From:     mjAddr{msg.From.Email, msg.From.Name},
		To:       []mjAddr{{msg.To.Email, msg.To.Name}},
		Subject:  msg.Subject,
		TextPart: msg.Text,
		HTMLPart: msg.HTML,
		CustomID: msg.InternalID, // echoed in webhooks and searchable for reconciliation
		Headers:  msg.Headers,
	}
	if msg.ReplyTo != nil && msg.ReplyTo.Email != "" {
		m.ReplyTo = &mjAddr{msg.ReplyTo.Email, msg.ReplyTo.Name}
	}
	sandbox, _ := cfg.Settings["sandbox"].(bool)
	raw := email.DoJSON(ctx, a.Client, http.MethodPost, base(cfg)+"/v3.1/send",
		sendRequest{Messages: []mjMessage{m}, SandboxMode: sandbox}, a.auth(cfg))
	res := email.ClassifyHTTP(raw)

	var body sendResponse
	parsed := json.Unmarshal(raw.Body, &body) == nil && len(body.Messages) > 0
	if res.Outcome == email.OutcomeAccepted {
		if parsed && body.Messages[0].Status != "success" {
			// HTTP 2xx but the message itself was refused.
			res.Outcome = email.OutcomeRejectedPermanent
			res.ErrorCode = "rejected"
		} else if parsed && len(body.Messages[0].To) > 0 {
			to := body.Messages[0].To[0]
			res.ProviderMessageID = to.MessageID.String()
			if res.ProviderMessageID == "" {
				res.ProviderMessageID = to.MessageUUID
			}
		}
	}
	if parsed && len(body.Messages[0].Errors) > 0 {
		e := body.Messages[0].Errors[0]
		res.ErrorCode = e.ErrorCode
		res.ErrorMessage = email.RedactString(e.ErrorMessage)
	}
	return res
}

func (a *Adapter) HealthCheck(ctx context.Context, cfg email.ProviderConfig) email.HealthResult {
	if cfg.Credentials["api_key"] == "" || cfg.Credentials["secret_key"] == "" {
		return email.HealthResult{OK: false, Message: "API key/secret secrets are not configured"}
	}
	raw := email.DoJSON(ctx, a.Client, http.MethodGet, base(cfg)+"/v3/REST/user", nil, a.auth(cfg))
	if raw.Status != http.StatusOK {
		msg := fmt.Sprintf("user check failed (HTTP %d)", raw.Status)
		if raw.Err != nil {
			msg = "user check failed: " + email.RedactString(raw.Err.Error())
		}
		return email.HealthResult{OK: false, Message: msg}
	}
	return email.HealthResult{OK: true, Message: "account reachable"}
}

// Lookup finds a message by the CustomID we set on submission.
func (a *Adapter) Lookup(ctx context.Context, cfg email.ProviderConfig, internalID, _ string, since time.Time) (email.LookupResult, error) {
	q := url.Values{}
	q.Set("CustomID", internalID)
	q.Set("FromTS", strconv.FormatInt(since.Add(-time.Hour).Unix(), 10))
	raw := email.DoJSON(ctx, a.Client, http.MethodGet, base(cfg)+"/v3/REST/message?"+q.Encode(), nil, a.auth(cfg))
	if raw.Err != nil || raw.Status != http.StatusOK {
		return email.LookupResult{}, fmt.Errorf("mailjet lookup failed: status %d", raw.Status)
	}
	var body struct {
		Count int `json:"Count"`
		Data  []struct {
			ID json.Number `json:"ID"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(raw.Body, &body); err != nil {
		return email.LookupResult{}, err
	}
	if len(body.Data) > 0 {
		return email.LookupResult{Status: email.LookupFound, ProviderMessageID: body.Data[0].ID.String()}, nil
	}
	return email.LookupResult{Status: email.LookupNotFound}, nil
}

func (a *Adapter) VerifyWebhook(r *http.Request, cfg email.ProviderConfig) error {
	return email.VerifySharedSecret(r, cfg.WebhookSecret)
}

type webhookEvent struct {
	Event       string      `json:"event"`
	Time        int64       `json:"time"`
	MessageID   json.Number `json:"MessageID"`
	MessageGUID string      `json:"Message_GUID"`
	Email       string      `json:"email"`
	CustomID    string      `json:"CustomID"`
	HardBounce  bool        `json:"hard_bounce"`
	Blocked     bool        `json:"blocked"`
	URL         string      `json:"url"`
	Error       string      `json:"error"`
}

func mapEvent(e webhookEvent) email.EventType {
	switch e.Event {
	case "sent":
		return email.EventDelivered // Mailjet "sent" = accepted by the recipient's mail server
	case "bounce":
		if e.HardBounce {
			return email.EventHardBounce
		}
		return email.EventSoftBounce
	case "blocked":
		return email.EventBlocked
	case "spam":
		return email.EventComplaint
	case "unsub":
		return email.EventUnsubscribed
	case "open":
		return email.EventOpened
	case "click":
		return email.EventClicked
	}
	return email.EventUnknown
}

func (a *Adapter) ParseWebhook(body []byte) ([]email.Event, error) {
	trimmed := strings.TrimSpace(string(body))
	var list []webhookEvent
	var rawList []map[string]any
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(body, &rawList)
	} else {
		var one webhookEvent
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, err
		}
		list = []webhookEvent{one}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		rawList = []map[string]any{m}
	}
	out := make([]email.Event, 0, len(list))
	for i, e := range list {
		occurred := time.Now().UTC()
		if e.Time > 0 {
			occurred = time.Unix(e.Time, 0).UTC()
		}
		h := sha256.Sum256([]byte(e.MessageGUID + "|" + e.MessageID.String() + "|" + e.Event + "|" +
			strconv.FormatInt(e.Time, 10) + "|" + e.URL))
		var payload map[string]any
		if i < len(rawList) {
			payload = email.RedactMap(rawList[i])
		}
		out = append(out, email.Event{
			DedupeKey:         hex.EncodeToString(h[:]),
			ProviderMessageID: e.MessageID.String(),
			InternalID:        e.CustomID,
			Email:             e.Email,
			Type:              mapEvent(e),
			RawType:           e.Event,
			OccurredAt:        occurred,
			Payload:           payload,
		})
	}
	return out, nil
}
