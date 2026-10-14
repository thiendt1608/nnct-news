package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"encore.app/news/brain"
)

func sample() Entry {
	return Entry{
		Rank: 1, Section: "ai", NSources: 7, Resend: true,
		Summary: brain.Summary{
			HeadlineVI:   "Tiêu đề <b>&</b> thử",
			Context:      "Bối cảnh: a < b && c > d.",
			KeyPoints:    []string{"Ý 1", "Ý 2 <script>", "Ý 3"},
			WhyItMatters: "Quan trọng & hay",
			Glossary:     []brain.Glossary{{Term: "RAG<>", Explain: "giải thích & ví dụ"}},
		},
		SourceNames: []string{"TechCrunch", "The Verge"},
	}
}

// AC8: golden + escape.
func TestRenderItemGolden(t *testing.T) {
	got := RenderItem(sample())
	want := "🔥 #1 · 🤖 AI · 7 nguồn · 🔄 Cập nhật\n\n" +
		"<b>Tiêu đề &lt;b&gt;&amp;&lt;/b&gt; thử</b>\n\n" +
		"Bối cảnh: a &lt; b &amp;&amp; c &gt; d.\n\n" +
		"🔍 Điểm chính\n• Ý 1\n• Ý 2 &lt;script&gt;\n• Ý 3\n\n" +
		"💡 Vì sao đáng quan tâm: Quan trọng &amp; hay\n\n" +
		"🧩 <i>RAG&lt;&gt;</i>: giải thích &amp; ví dụ\n\n" +
		"📰 TechCrunch · The Verge"
	if got != want {
		t.Fatalf("\n%s\n---\n%s", got, want)
	}
}

func TestRenderFallback(t *testing.T) {
	out := RenderItem(Entry{Rank: 3, Section: "vn", NSources: 1, Fallback: true,
		Summary: brain.FallbackSummary("Tiêu đề", "Trích đoạn"), SourceNames: []string{"Genk"}})
	if !strings.Contains(out, "⚠️") || !strings.Contains(out, "Trích đoạn") || strings.Contains(out, "🔥") || strings.Contains(out, "nguồn") {
		t.Fatal(out)
	}
}

func TestRenderTruncatesTo4096(t *testing.T) {
	e := sample()
	long := strings.Repeat("Một câu rất dài. ", 120)
	e.Summary.Context = long
	e.Summary.KeyPoints = []string{long, long, long, long, long}
	out := RenderItem(e)
	if n := utf8.RuneCountInString(out); n > MaxMessage {
		t.Fatalf("%d > %d", n, MaxMessage)
	}
	if !strings.Contains(out, "<b>") {
		t.Fatal("headline phải còn")
	}
}

func TestMarkupAndHeader(t *testing.T) {
	b, _ := json.Marshal(ItemMarkup("https://x/r/1"))
	if string(b) != `{"inline_keyboard":[[{"text":"📖 Đọc bài gốc","url":"https://x/r/1"}]]}` {
		t.Fatal(string(b))
	}
	h := RenderHeader(time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC), 20, map[string]int{"ai": 6, "tech": 6, "vn": 5, "dev": 3}, 3)
	if !strings.Contains(h, "thứ Năm 15/10 — 20 tin") || !strings.Contains(h, "🔥 Nóng (3) · 🤖 AI (6) · 🌍 Tech (6) · 🛠 Dev (3) · 🇻🇳 VN (5)") {
		t.Fatal(h)
	}
}

func TestSendMessageAndRetryAfter(t *testing.T) {
	calls := 0
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/botSECRETTOKEN/sendMessage" {
			t.Errorf("path %s", r.URL.Path)
		}
		if calls == 1 {
			fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":2}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":42}}`)
	}))
	defer srv.Close()
	var slept []time.Duration
	c := &Client{BaseURL: srv.URL, Token: func() string { return "SECRETTOKEN" }, ChatID: func() string { return "123" },
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	id, err := c.SendMessage(context.Background(), "hi", ItemMarkup("https://x"))
	if err != nil || id != 42 {
		t.Fatalf("%v %d", err, id)
	}
	if len(slept) != 1 || slept[0] < 2*time.Second {
		t.Fatalf("phải chờ retry_after: %v", slept)
	}
	if gotBody["disable_notification"] != true || gotBody["parse_mode"] != "HTML" || gotBody["chat_id"] != "123" {
		t.Fatalf("%v", gotBody)
	}
	if lp, _ := gotBody["link_preview_options"].(map[string]any); lp["is_disabled"] != true {
		t.Fatal("phải tắt preview")
	}
}

func TestErrorsDoNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`)
	}))
	url := srv.URL
	srv.Close() // kết nối thất bại → url.Error chứa URL có token
	c := &Client{BaseURL: url, Token: func() string { return "SECRETTOKEN" }, ChatID: func() string { return "1" }}
	_, err := c.SendMessage(context.Background(), "x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatalf("lộ token: %v", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request"}`)
	}))
	defer srv2.Close()
	c.BaseURL = srv2.URL
	if _, err := c.SendMessage(context.Background(), "x", nil); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatal(err)
	}
}
