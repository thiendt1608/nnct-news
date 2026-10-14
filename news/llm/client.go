// Package llm is the shared Gemini client: per-model throttling (requests and tokens),
// retry on 429/5xx, a persisted daily budget and a model fallback chain.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // RPD của Google reset theo giờ Pacific

	"golang.org/x/time/rate"
)

// ErrBudgetExhausted means every model in the chain is out of budget or quota right now.
// Callers should leave work in its current state and retry on the next run.
var ErrBudgetExhausted = errors.New("llm: hết budget/quota ở mọi model trong chuỗi")

// ErrBadJSON means the model answered but not with JSON matching the target type.
var ErrBadJSON = errors.New("llm: JSON không hợp lệ")

type HTTPError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300] + "…"
	}
	return fmt.Sprintf("gemini http %d: %s", e.Status, b)
}

type ModelCfg struct {
	MinInterval  time.Duration
	TokensPerMin int
	DailyBudget  int
}

// Usage persists request counts per (Pacific day, model).
type Usage interface {
	Used(ctx context.Context, day, model string) (int, error)
	Add(ctx context.Context, day, model string, n int) error
}

type Client struct {
	HTTP    *http.Client
	BaseURL string // default https://generativelanguage.googleapis.com/v1beta
	APIKey  func() string
	Models  map[string]ModelCfg
	Usage   Usage
	Backoff []time.Duration // default 15s,30s,60s,120s
	Sleep   func(ctx context.Context, d time.Duration) error
	Now     func() time.Time

	mu   sync.Mutex
	reqL map[string]*rate.Limiter
	tokL map[string]*rate.Limiter
}

var pacific = func() *time.Location {
	l, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return l
}()

// DayPacific returns the quota day (YYYY-MM-DD, Pacific time) of t.
func DayPacific(t time.Time) string { return t.In(pacific).Format("2006-01-02") }

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://generativelanguage.googleapis.com/v1beta"
}

func (c *Client) backoff() []time.Duration {
	if c.Backoff != nil {
		return c.Backoff
	}
	return []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second}
}

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

func (c *Client) limiters(model string) (*rate.Limiter, *rate.Limiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reqL == nil {
		c.reqL, c.tokL = map[string]*rate.Limiter{}, map[string]*rate.Limiter{}
	}
	if l, ok := c.reqL[model]; ok {
		return l, c.tokL[model]
	}
	cfg := c.Models[model]
	rl := rate.NewLimiter(rate.Inf, 1)
	if cfg.MinInterval > 0 {
		rl = rate.NewLimiter(rate.Every(cfg.MinInterval), 1)
	}
	tl := rate.NewLimiter(rate.Inf, 1)
	if cfg.TokensPerMin > 0 {
		tl = rate.NewLimiter(rate.Limit(float64(cfg.TokensPerMin)/60), cfg.TokensPerMin)
	}
	c.reqL[model], c.tokL[model] = rl, tl
	return rl, tl
}

func (c *Client) throttle(ctx context.Context, model string, tokens int) error {
	rl, tl := c.limiters(model)
	if err := rl.Wait(ctx); err != nil {
		return err
	}
	if b := tl.Burst(); tl.Limit() != rate.Inf && b > 0 {
		if tokens > b {
			tokens = b
		}
		if tokens < 1 {
			tokens = 1
		}
		return tl.WaitN(ctx, tokens)
	}
	return nil
}

// Remaining returns how many requests the model may still make today.
func (c *Client) Remaining(ctx context.Context, model string) (int, error) {
	used, err := c.Usage.Used(ctx, DayPacific(c.now()), model)
	if err != nil {
		return 0, err
	}
	r := c.Models[model].DailyBudget - used
	if r < 0 {
		r = 0
	}
	return r, nil
}

// EstimateTokens is a rough token count used only for throttling.
func EstimateTokens(s string) int { return len(s)/3 + 1 }

// post sends one request with throttle + retry. cost = how many quota requests one attempt uses.
func (c *Client) post(ctx context.Context, model, method string, body any, tokens, cost int) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	backs := c.backoff()
	var last error
	for attempt := 0; attempt <= len(backs); attempt++ {
		rem, err := c.Remaining(ctx, model)
		if err != nil {
			return nil, err
		}
		if rem < cost {
			return nil, ErrBudgetExhausted
		}
		if err := c.throttle(ctx, model, tokens); err != nil {
			return nil, err
		}
		if err := c.Usage.Add(ctx, DayPacific(c.now()), model, cost); err != nil {
			return nil, err
		}
		out, err := c.do(ctx, model, method, payload)
		if err == nil {
			return out, nil
		}
		last = err
		var he *HTTPError
		if !errors.As(err, &he) || (he.Status != 429 && he.Status < 500) {
			// lỗi mạng cũng retry; 4xx khác 429 thì không
			if he != nil {
				return nil, err
			}
		}
		if attempt == len(backs) {
			break
		}
		d := backs[attempt]
		if he != nil && he.RetryAfter > d {
			d = he.RetryAfter
		}
		d += time.Duration(rand.Int63n(int64(d)/5 + 1))
		if err := c.sleep(ctx, d); err != nil {
			return nil, err
		}
	}
	return nil, last
}

func (c *Client) do(ctx context.Context, model, method string, payload []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/models/%s:%s", c.baseURL(), model, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey())
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode, Body: string(b), RetryAfter: parseRetryDelay(b)}
	}
	return b, nil
}

func parseRetryDelay(body []byte) time.Duration {
	var e struct {
		Error struct {
			Details []struct {
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return 0
	}
	for _, d := range e.Error.Details {
		if d.RetryDelay != "" {
			if dur, err := time.ParseDuration(d.RetryDelay); err == nil {
				return dur
			}
		}
	}
	return 0
}

// GenerateJSON asks the first usable model in chain for JSON matching schema and decodes it
// into out. It returns the model that answered.
func (c *Client) GenerateJSON(ctx context.Context, chain []string, system, user string, schema map[string]any, out any) (string, error) {
	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": user}}}},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"responseSchema":   schema,
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": system}}}
	}
	tokens := EstimateTokens(system) + EstimateTokens(user)
	var firstHard error
	exhausted := false
	for _, m := range chain {
		raw, err := c.post(ctx, m, "generateContent", body, tokens, 1)
		if err == nil {
			var text string
			text, err = extractText(raw)
			if err == nil {
				if err = json.Unmarshal([]byte(text), out); err == nil {
					return m, nil
				}
				err = fmt.Errorf("%w: %v", ErrBadJSON, err)
			}
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var he *HTTPError
		switch {
		case errors.Is(err, ErrBudgetExhausted), errors.As(err, &he) && (he.Status == 429 || he.Status >= 500):
			exhausted = true
		default:
			if firstHard == nil {
				firstHard = fmt.Errorf("%s: %w", m, err)
			}
		}
	}
	if firstHard != nil {
		return "", firstHard
	}
	if exhausted {
		return "", ErrBudgetExhausted
	}
	return "", errors.New("llm: chain rỗng")
}

func extractText(raw []byte) (string, error) {
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadJSON, err)
	}
	if len(r.Candidates) == 0 {
		return "", fmt.Errorf("%w: không có candidate", ErrBadJSON)
	}
	var sb strings.Builder
	for _, p := range r.Candidates[0].Content.Parts {
		if !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("%w: rỗng (finish=%s)", ErrBadJSON, r.Candidates[0].FinishReason)
	}
	return sb.String(), nil
}

// Embed returns one vector per text. Each text costs one request of daily budget
// (đo 2026-10-14: batch N text tính N request). No fallback model: vectors of
// different models are not comparable.
func (c *Client) Embed(ctx context.Context, model string, texts []string, dim int) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	reqs := make([]any, len(texts))
	tokens := 0
	for i, t := range texts {
		tokens += EstimateTokens(t)
		reqs[i] = map[string]any{
			"model":                "models/" + model,
			"content":              map[string]any{"parts": []any{map[string]any{"text": t}}},
			"outputDimensionality": dim,
		}
	}
	raw, err := c.post(ctx, model, "batchEmbedContents", map[string]any{"requests": reqs}, tokens, len(texts))
	if err != nil {
		return nil, err
	}
	var r struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadJSON, err)
	}
	if len(r.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: nhận %d vector cho %d text", len(r.Embeddings), len(texts))
	}
	out := make([][]float32, len(texts))
	for i, e := range r.Embeddings {
		if len(e.Values) != dim {
			return nil, fmt.Errorf("embed: vector %d có %d chiều, cần %d", i, len(e.Values), dim)
		}
		out[i] = e.Values
	}
	return out, nil
}
