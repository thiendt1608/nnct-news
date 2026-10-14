// Package telegram is a minimal Bot API client (net/http) plus message rendering.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string // default https://api.telegram.org
	Token   func() string
	ChatID  func() string
	Sleep   func(ctx context.Context, d time.Duration) error
	// MaxRetries is how many times a 429 is retried (default 3).
	MaxRetries int
}

type Button struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

type Markup struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}

type apiResp struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// APIError is a non-ok Telegram response.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram %d: %s", e.Code, e.Description) }

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// call posts to a Bot API method, honoring retry_after on 429. Errors never contain the token.
func (c *Client) call(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	token := c.Token()
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	retries := c.MaxRetries
	if retries == 0 {
		retries = 3
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", base, token, method), bytes.NewReader(body))
		if err != nil {
			return nil, errors.New(redact(err.Error(), token))
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, errors.New(redact(err.Error(), token))
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		var r apiResp
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("telegram http %d: phản hồi không phải JSON", resp.StatusCode)
		}
		if r.OK {
			return r.Result, nil
		}
		if r.ErrorCode == 429 && attempt < retries {
			wait := time.Duration(r.Parameters.RetryAfter) * time.Second
			if wait <= 0 {
				wait = time.Second
			}
			if err := c.sleep(ctx, wait+200*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		return nil, &APIError{Code: r.ErrorCode, Description: r.Description}
	}
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<token>")
}

// SendMessage sends a silent HTML message with link preview disabled; returns message_id.
func (c *Client) SendMessage(ctx context.Context, text string, markup *Markup) (int64, error) {
	p := map[string]any{
		"chat_id":              c.ChatID(),
		"text":                 text,
		"parse_mode":           "HTML",
		"disable_notification": true,
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if markup != nil {
		p["reply_markup"] = markup
	}
	res, err := c.call(ctx, "sendMessage", p)
	if err != nil {
		return 0, err
	}
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(res, &m); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

func (c *Client) EditMessageReplyMarkup(ctx context.Context, messageID int64, markup *Markup) error {
	_, err := c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": c.ChatID(), "message_id": messageID, "reply_markup": markup})
	return err
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	_, err := c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text})
	return err
}

func (c *Client) SetWebhook(ctx context.Context, url, secret string) error {
	_, err := c.call(ctx, "setWebhook", map[string]any{"url": url, "secret_token": secret, "allowed_updates": []string{"message", "callback_query"}})
	return err
}
