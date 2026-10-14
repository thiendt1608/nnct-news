package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"encore.app/news/config"
)

func TestCanonicalURL(t *testing.T) {
	cases := map[string]string{
		"https://www.example.com/a/b/?utm_source=x&id=3&fbclid=zz#frag": "https://example.com/a/b?id=3",
		"http://m.vnexpress.net/bai-viet-123.html":                      "https://vnexpress.net/bai-viet-123.html",
		"https://example.com/post/amp":                                  "https://example.com/post",
		"https://example.com/post/amp/":                                 "https://example.com/post",
		"https://example.com/post/":                                     "https://example.com/post",
		"https://EXAMPLE.com/":                                          "https://example.com/",
		"https://example.com/a?b=2&a=1&utm_campaign=q":                  "https://example.com/a?a=1&b=2",
	}
	for in, want := range cases {
		got, err := CanonicalURL(in)
		if err != nil || got != want {
			t.Errorf("%s -> %q (%v), want %q", in, got, err, want)
		}
	}
}

func TestTitleHashAndDedupe(t *testing.T) {
	if TitleHash("OpenAI ra mắt GPT-6!") != TitleHash("  openai ra mắt gpt 6 ") {
		t.Fatal("hash phải bỏ qua hoa thường/dấu câu")
	}
	if TitleHash("a") == TitleHash("b") {
		t.Fatal("hash khác nhau")
	}
	a, _ := Build("s", "en", "https://x.com/1", "Same title", "", time.Now())
	b, _ := Build("s", "en", "https://x.com/1?utm_source=t", "Other", "", time.Now())
	c, _ := Build("s", "en", "https://y.com/2", "same  TITLE!", "", time.Now())
	d, _ := Build("s", "en", "https://y.com/3", "Distinct", "", time.Now())
	if got := Dedupe([]Item{a, b, c, d}); len(got) != 2 {
		t.Fatalf("dedupe: %d", len(got))
	}
}

func TestCleanSnippetAndFresh(t *testing.T) {
	if got := CleanSnippet("<p>Hello &amp; <b>bye</b></p>", 100); got != "Hello & bye" {
		t.Fatalf("%q", got)
	}
	if got := CleanSnippet("xin chào thế giới", 3); got != "xin" {
		t.Fatalf("%q", got)
	}
	now := time.Now()
	if Fresh(Item{PublishedAt: now.Add(-49 * time.Hour)}, now, 48*time.Hour) {
		t.Fatal("quá cũ")
	}
	if !Fresh(Item{}, now, 48*time.Hour) {
		t.Fatal("không có ngày thì coi là mới")
	}
}

const rssFixture = `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Tin A &amp; B</title><link>https://www.example.com/a/?utm_source=rss</link><description>&lt;p&gt;Nội dung&lt;/p&gt;</description><pubDate>Wed, 14 Oct 2026 01:00:00 +0000</pubDate></item>
<item><title>Tin C</title><link>https://example.com/c</link><pubDate>Wed, 14 Oct 2026 02:00:00 +0000</pubDate></item>
<item><title></title><link>https://example.com/empty</link></item>
</channel></rss>`

func TestFetchRSSAndNotModified(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") == "" {
			t.Error("thiếu User-Agent")
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, rssFixture)
	}))
	defer srv.Close()
	src := config.Source{ID: "s", Kind: "rss", URL: srv.URL, Lang: "vi"}
	res, err := Fetch(context.Background(), srv.Client(), src, FetchState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.State.ETag != `"v1"` {
		t.Fatalf("items=%d etag=%q", len(res.Items), res.State.ETag)
	}
	if res.Items[0].URLCanonical != "https://example.com/a" || res.Items[0].Snippet != "Nội dung" || res.Items[0].Title != "Tin A & B" {
		t.Fatalf("%+v", res.Items[0])
	}
	res2, err := Fetch(context.Background(), srv.Client(), src, res.State)
	if err != nil || !res2.NotModified || len(res2.Items) != 0 {
		t.Fatalf("304: %+v %v", res2, err)
	}
}

func TestFetchRSSErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			fmt.Fprint(w, "not xml at all")
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	for _, p := range []string{"/500", "/bad"} {
		if _, err := FetchRSS(context.Background(), srv.Client(), config.Source{ID: "s", URL: srv.URL + p}, FetchState{}); err == nil {
			t.Errorf("%s: kỳ vọng lỗi", p)
		}
	}
}

func TestFetchHN(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/topstories.json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[1,2,3,4]`) })
	mux.HandleFunc("/v0/item/1.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":1,"type":"story","title":"Hot","url":"https://a.com/x","score":300,"descendants":50,"time":1760400000}`)
	})
	mux.HandleFunc("/v0/item/2.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":2,"type":"story","title":"Low","url":"https://a.com/y","score":10,"time":1760400000}`)
	})
	mux.HandleFunc("/v0/item/3.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":3,"type":"story","title":"AskHN","score":500,"time":1760400000}`) // no url
	})
	// item 4 -> 404
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := FetchHN(context.Background(), srv.Client(), config.Source{ID: "hn", Kind: "hn", URL: srv.URL + "/v0/topstories.json", MinPoints: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Engagement["points"] != 300 {
		t.Fatalf("%+v", res.Items)
	}
}
