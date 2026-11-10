// Package whatsapp sends messages through the Meta WhatsApp Cloud API
// (POST https://graph.facebook.com/{version}/{phone-number-id}/messages).
// Free-form text is only delivered within 24 hours of the customer's last
// message; outside that window only approved templates are.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Sender sends WhatsApp messages from one of the business's numbers
// (fromPhoneNumberID) to a customer (to: digits with country code, e.g.
// 919876543210). Each returns the WhatsApp message id (wamid).
type Sender interface {
	SendText(ctx context.Context, fromPhoneNumberID, to, body string) (string, error)
	SendTemplate(ctx context.Context, fromPhoneNumberID, to, name, language string, params []string) (string, error)
}

// Client is the Cloud API Sender.
type Client struct {
	BaseURL string // https://graph.facebook.com
	Version string // e.g. v23.0
	Token   string
	HTTP    *http.Client
}

func NewClient(token, version string) *Client {
	return &Client{
		BaseURL: "https://graph.facebook.com",
		Version: version,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) SendText(ctx context.Context, from, to, body string) (string, error) {
	return c.send(ctx, from, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text":              map[string]any{"body": body, "preview_url": false},
	})
}

func (c *Client) SendTemplate(ctx context.Context, from, to, name, language string, params []string) (string, error) {
	tmpl := map[string]any{"name": name, "language": map[string]string{"code": language}}
	if len(params) > 0 {
		ps := make([]map[string]string, len(params))
		for i, p := range params {
			ps[i] = map[string]string{"type": "text", "text": p}
		}
		tmpl["components"] = []map[string]any{{"type": "body", "parameters": ps}}
	}
	return c.send(ctx, from, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template":          tmpl,
	})
}

// APIError is an error response from the Cloud API.
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("whatsapp api: status %d, code %d: %s", e.Status, e.Code, e.Message)
}

// Retryable reports whether sending again later may succeed (rate limits
// and server errors).
func (e *APIError) Retryable() bool { return e.Status == http.StatusTooManyRequests || e.Status >= 500 }

func (c *Client) send(ctx context.Context, from string, payload map[string]any) (string, error) {
	if c.Token == "" {
		return "", errors.New("whatsapp sending is not configured (WHATSAPP_TOKEN)")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/%s/%s/messages", c.BaseURL, c.Version, from)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return "", &APIError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	var ok struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &ok); err != nil || len(ok.Messages) == 0 {
		return "", fmt.Errorf("whatsapp api: unexpected response %q", truncate(string(body), 200))
	}
	return ok.Messages[0].ID, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Log is a Sender for development without WhatsApp credentials: it logs
// instead of sending.
type Log struct{ Logger *slog.Logger }

func (l Log) SendText(ctx context.Context, from, to, body string) (string, error) {
	l.Logger.InfoContext(ctx, "whatsapp (not sent)", slog.String("from", from), slog.String("to", to), slog.String("text", body))
	return "", nil
}

func (l Log) SendTemplate(ctx context.Context, from, to, name, language string, params []string) (string, error) {
	l.Logger.InfoContext(ctx, "whatsapp template (not sent)", slog.String("from", from), slog.String("to", to),
		slog.String("template", name), slog.String("language", language), slog.Any("params", params))
	return "", nil
}
