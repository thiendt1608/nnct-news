package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type memUsage struct {
	mu sync.Mutex
	m  map[string]int
}

func (u *memUsage) Used(_ context.Context, day, model string) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.m[day+model], nil
}
func (u *memUsage) Add(_ context.Context, day, model string, n int) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.m[day+model] += n
	return nil
}

type reply struct {
	status int
	body   string
}

// fakeTransport answers per model according to scripted replies (last one repeats).
type fakeTransport struct {
	mu      sync.Mutex
	script  map[string][]reply
	calls   map[string]int
	total   int
	lastKey string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total++
	f.lastKey = r.Header.Get("x-goog-api-key")
	model := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":generateContent")
	model = strings.TrimSuffix(model, ":batchEmbedContents")
	i := f.calls[model]
	f.calls[model]++
	s := f.script[model]
	rep := s[len(s)-1]
	if i < len(s) {
		rep = s[i]
	}
	return &http.Response{StatusCode: rep.status, Body: io.NopCloser(strings.NewReader(rep.body)), Header: http.Header{}}, nil
}

func ok(text string) reply {
	return reply{200, fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%q}]}}]}`, text)}
}

func newClient(script map[string][]reply, budget int) (*Client, *fakeTransport, *[]time.Duration) {
	ft := &fakeTransport{script: script, calls: map[string]int{}}
	var sleeps []time.Duration
	c := &Client{
		HTTP:    &http.Client{Transport: ft},
		BaseURL: "http://fake/v1beta",
		APIKey:  func() string { return "test-key" },
		Models: map[string]ModelCfg{
			"a": {DailyBudget: budget}, "b": {DailyBudget: budget}, "emb": {DailyBudget: budget},
		},
		Usage:   &memUsage{m: map[string]int{}},
		Backoff: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
		Sleep:   func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil },
	}
	return c, ft, &sleeps
}

type out struct {
	X string `json:"x"`
}

func TestRetryThen200(t *testing.T) {
	c, ft, sleeps := newClient(map[string][]reply{"a": {{429, `{"error":{"details":[{"retryDelay":"1s"}]}}`}, {500, "boom"}, ok(`{"x":"hi"}`)}}, 10)
	var o out
	m, err := c.GenerateJSON(context.Background(), []string{"a"}, "", "q", map[string]any{}, &o)
	if err != nil || m != "a" || o.X != "hi" {
		t.Fatalf("%v %s %+v", err, m, o)
	}
	if ft.calls["a"] != 3 || len(*sleeps) != 2 {
		t.Fatalf("calls=%d sleeps=%v", ft.calls["a"], *sleeps)
	}
	if (*sleeps)[0] < time.Second {
		t.Fatalf("phải tôn trọng retryDelay 1s: %v", (*sleeps)[0])
	}
	if ft.lastKey != "test-key" {
		t.Fatal("key phải đi qua header")
	}
	if used, _ := c.Usage.Used(context.Background(), DayPacific(time.Now()), "a"); used != 3 {
		t.Fatalf("usage=%d", used)
	}
}

// AC4: 429 mãi mãi ở model a -> fallback sang b; không vượt budget.
func TestFallbackOn429(t *testing.T) {
	c, ft, _ := newClient(map[string][]reply{"a": {{429, "quota"}}, "b": {ok(`{"x":"from-b"}`)}}, 10)
	var o out
	m, err := c.GenerateJSON(context.Background(), []string{"a", "b"}, "", "q", map[string]any{}, &o)
	if err != nil || m != "b" || o.X != "from-b" {
		t.Fatalf("%v %s %+v", err, m, o)
	}
	if ft.calls["a"] != 3 { // 1 + 2 retry
		t.Fatalf("a calls=%d", ft.calls["a"])
	}
}

func TestAll429ReturnsExhausted(t *testing.T) {
	c, _, _ := newClient(map[string][]reply{"a": {{429, "q"}}, "b": {{429, "q"}}}, 100)
	var o out
	_, err := c.GenerateJSON(context.Background(), []string{"a", "b"}, "", "q", map[string]any{}, &o)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("%v", err)
	}
}

func TestBudgetStopsBeforeCalling(t *testing.T) {
	c, ft, _ := newClient(map[string][]reply{"a": {ok(`{"x":"1"}`)}, "b": {ok(`{"x":"2"}`)}}, 1)
	var o out
	for i := 0; i < 2; i++ {
		if _, err := c.GenerateJSON(context.Background(), []string{"a", "b"}, "", "q", map[string]any{}, &o); err != nil {
			t.Fatal(err)
		}
	}
	// a và b mỗi cái 1 request, giờ hết sạch
	if _, err := c.GenerateJSON(context.Background(), []string{"a", "b"}, "", "q", map[string]any{}, &o); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("%v", err)
	}
	if ft.total != 2 {
		t.Fatalf("không được gọi HTTP khi hết budget: total=%d", ft.total)
	}
}

func TestBadRequestNotRetried(t *testing.T) {
	c, ft, _ := newClient(map[string][]reply{"a": {{400, "bad schema"}}}, 10)
	var o out
	_, err := c.GenerateJSON(context.Background(), []string{"a"}, "", "q", map[string]any{}, &o)
	if err == nil || errors.Is(err, ErrBudgetExhausted) || ft.calls["a"] != 1 {
		t.Fatalf("err=%v calls=%d", err, ft.calls["a"])
	}
}

func TestBadJSONFallsThrough(t *testing.T) {
	c, _, _ := newClient(map[string][]reply{"a": {ok(`not json`)}, "b": {ok(`{"x":"ok"}`)}}, 10)
	var o out
	m, err := c.GenerateJSON(context.Background(), []string{"a", "b"}, "", "q", map[string]any{}, &o)
	if err != nil || m != "b" {
		t.Fatalf("%v %s", err, m)
	}
	c2, _, _ := newClient(map[string][]reply{"a": {ok(`not json`)}}, 10)
	if _, err := c2.GenerateJSON(context.Background(), []string{"a"}, "", "q", map[string]any{}, &o); !errors.Is(err, ErrBadJSON) {
		t.Fatalf("%v", err)
	}
}

func TestThoughtPartsIgnored(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"suy nghĩ","thought":true},{"text":"{\"x\":\"ans\"}","thoughtSignature":"abc"}]}}]}`
	c, _, _ := newClient(map[string][]reply{"a": {{200, body}}}, 10)
	var o out
	if _, err := c.GenerateJSON(context.Background(), []string{"a"}, "sys", "q", map[string]any{}, &o); err != nil || o.X != "ans" {
		t.Fatalf("%v %+v", err, o)
	}
}

func TestEmbed(t *testing.T) {
	vec := func() string {
		s := make([]string, 4)
		for i := range s {
			s[i] = "0.5"
		}
		return "[" + strings.Join(s, ",") + "]"
	}
	body := fmt.Sprintf(`{"embeddings":[{"values":%s},{"values":%s},{"values":%s}]}`, vec(), vec(), vec())
	c, _, _ := newClient(map[string][]reply{"emb": {{200, body}}}, 5)
	vs, err := c.Embed(context.Background(), "emb", []string{"a", "b", "c"}, 4)
	if err != nil || len(vs) != 3 || len(vs[0]) != 4 {
		t.Fatalf("%v %v", err, vs)
	}
	if r, _ := c.Remaining(context.Background(), "emb"); r != 2 {
		t.Fatalf("batch 3 text phải tốn 3 request, còn %d", r)
	}
	// 3 text nữa > còn 2 -> hết budget, không gọi HTTP
	if _, err := c.Embed(context.Background(), "emb", []string{"a", "b", "c"}, 4); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("%v", err)
	}
	// sai chiều
	c2, _, _ := newClient(map[string][]reply{"emb": {{200, `{"embeddings":[{"values":[1,2]}]}`}}}, 5)
	if _, err := c2.Embed(context.Background(), "emb", []string{"a"}, 4); err == nil {
		t.Fatal("kỳ vọng lỗi sai chiều")
	}
}

func TestDayPacific(t *testing.T) {
	// 2026-10-14 06:00 UTC = 2026-10-13 23:00 PDT
	if got := DayPacific(time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)); got != "2026-10-13" {
		t.Fatal(got)
	}
	if got := DayPacific(time.Date(2026, 10, 14, 7, 30, 0, 0, time.UTC)); got != "2026-10-14" {
		t.Fatal(got)
	}
}

func TestThrottleSpacesRequests(t *testing.T) {
	c, _, _ := newClient(map[string][]reply{"a": {ok(`{"x":"1"}`)}}, 10)
	c.Models["a"] = ModelCfg{MinInterval: 60 * time.Millisecond, DailyBudget: 10}
	var o out
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.GenerateJSON(context.Background(), []string{"a"}, "", "q", map[string]any{}, &o); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) < 110*time.Millisecond {
		t.Fatalf("limiter không giãn nhịp: %v", time.Since(start))
	}
}
