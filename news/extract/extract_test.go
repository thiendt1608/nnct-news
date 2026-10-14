package extract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Fixtures dưới testdata/ là HTML tự viết mô phỏng cấu trúc báo (nav + article + footer),
// không phải bản lưu của trang thật.
func TestFromHTMLArticle(t *testing.T) {
	b, _ := os.ReadFile("testdata/vnexpress_like.html")
	u, _ := url.Parse("https://example.com/a")
	r, err := FromHTML(b, u)
	if err != nil || r.LowContext {
		t.Fatalf("%v low=%v len=%d", err, r.LowContext, len(r.Text))
	}
	if !strings.Contains(r.Text, "mô hình mới") || strings.Contains(r.Text, "quảng cáo") {
		t.Fatalf("nội dung sai: %.200s", r.Text)
	}
}

func TestPaywallIsLowContext(t *testing.T) {
	b, _ := os.ReadFile("testdata/paywall_like.html")
	u, _ := url.Parse("https://example.com/p")
	r, err := FromHTML(b, u)
	if err != nil || !r.LowContext {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestTruncates(t *testing.T) {
	big := "<html><body><article><h1>T</h1>" + strings.Repeat("<p>"+strings.Repeat("từ ", 200)+"</p>", 100) + "</article></body></html>"
	u, _ := url.Parse("https://example.com/big")
	r, err := FromHTML([]byte(big), u)
	if err != nil || len([]rune(r.Text)) != MaxChars {
		t.Fatalf("%v len=%d", err, len([]rune(r.Text)))
	}
}

func TestFetchErrorsAreLowContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(404)
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF"))
		default:
			b, _ := os.ReadFile("testdata/vnexpress_like.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
		}
	}))
	defer srv.Close()
	c := NewClient(true)
	if r, err := Fetch(context.Background(), c, srv.URL+"/ok"); err != nil || r.LowContext {
		t.Fatalf("ok: %v %+v", err, r)
	}
	for _, p := range []string{"/404", "/pdf"} {
		if r, err := Fetch(context.Background(), c, srv.URL+p); err == nil || !r.LowContext {
			t.Errorf("%s: kỳ vọng lỗi + low_context", p)
		}
	}
	if r, err := Fetch(context.Background(), c, "file:///etc/passwd"); err == nil || !r.LowContext {
		t.Error("scheme file phải bị từ chối")
	}
}

func TestBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer srv.Close()
	if _, err := Fetch(context.Background(), NewClient(false), srv.URL); err == nil {
		t.Fatal("loopback phải bị chặn")
	}
}
