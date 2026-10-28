package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"time"
)

// HTTPResult captures what happened on the wire for one provider API call.
type HTTPResult struct {
	Status       int
	Body         []byte
	Header       http.Header
	WroteRequest bool // the full request was written to the connection
	Err          error
}

// DoJSON performs a JSON request and records, via httptrace, whether the
// request was fully written before any error. That is the line between
// "definitely not sent" and "acceptance unknown".
func DoJSON(ctx context.Context, client *http.Client, method, url string, payload any, setHeaders func(http.Header)) HTTPResult {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return HTTPResult{Err: err}
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return HTTPResult{Err: err}
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if setHeaders != nil {
		setHeaders(req.Header)
	}
	var wrote bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			wrote = true
		}
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResult{WroteRequest: wrote, Err: err}
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return HTTPResult{Status: resp.StatusCode, Body: b, Header: resp.Header, WroteRequest: true, Err: rerr}
}

// ClassifyHTTP maps a raw HTTP result onto a submission outcome. Adapters call
// it and then fill in provider-specific details (message ID, error code).
func ClassifyHTTP(r HTTPResult) SendResult {
	res := SendResult{HTTPStatus: r.Status}
	if r.Status == 0 {
		// No HTTP response at all.
		if r.Err != nil {
			res.ErrorMessage = RedactString(r.Err.Error())
		}
		if r.WroteRequest {
			res.Outcome = OutcomeUnknown
			res.ErrorCode = "timeout_after_submit"
		} else {
			res.Outcome = OutcomeNotSent
			res.ErrorCode = "connection_failed"
		}
		return res
	}
	res.Response = RedactJSON(r.Body)
	switch {
	case r.Status >= 200 && r.Status < 300:
		res.Outcome = OutcomeAccepted
	case r.Status == http.StatusTooManyRequests:
		res.Outcome = OutcomeRateLimited
		res.RetryAfter = parseRetryAfter(r.Header)
		res.ErrorCode = "rate_limited"
	case r.Status == http.StatusRequestTimeout:
		res.Outcome = OutcomeRejectedTransient
		res.ErrorCode = "request_timeout"
	case r.Status == http.StatusGatewayTimeout:
		// A gateway timed out waiting for the upstream that may have accepted it.
		res.Outcome = OutcomeUnknown
		res.ErrorCode = "gateway_timeout"
	case r.Status >= 500:
		res.Outcome = OutcomeRejectedTransient
		res.ErrorCode = "provider_unavailable"
	case r.Status == http.StatusPaymentRequired:
		res.Outcome = OutcomeRejectedPermanent
		res.ErrorCode = "quota_exhausted"
		res.QuotaExhausted = true
	case r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden:
		res.Outcome = OutcomeRejectedPermanent
		res.ErrorCode = "auth_failed"
	default:
		res.Outcome = OutcomeRejectedPermanent
		res.ErrorCode = "rejected"
	}
	if res.Outcome != OutcomeAccepted {
		res.ErrorMessage = truncate(RedactString(string(r.Body)), 500)
	}
	return res
}

func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := h.Get("Retry-After")
	if v == "" {
		v = h.Get("X-RateLimit-Reset") // Brevo: seconds until reset
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

// IsTimeout reports whether err is a deadline/timeout.
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne interface{ Timeout() bool }
	return errors.As(err, &ne) && ne.Timeout()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
