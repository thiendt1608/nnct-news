package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"encore.app/news/brain"
	"encore.app/news/config"
	"encore.app/news/extract"
	"encore.app/news/llm"
	"encore.app/news/telegram"
)

func resetDB(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"feedback", "digest_items", "digests", "items", "clusters", "sources", "llm_usage", "profile"} {
		if _, err := db.Exec(context.Background(), "DELETE FROM "+tbl); err != nil { // role của test không có TRUNCATE
			t.Fatal(err)
		}
	}
}

// ---- fakes ----

type fakeLLM struct {
	mu           sync.Mutex
	summaryErr   error // lỗi trả về cho mọi lần tóm tắt
	triageCalls  int
	summaryCalls int
	embedTexts   int
	embedBudget  int // 0 = vô hạn
}

var idRe = regexp.MustCompile(`id=(\d+) \| nguồn=[^|]* \| tiêu đề: ([^|]*) \|`)

func (f *fakeLLM) GenerateJSON(_ context.Context, _ []string, system, user string, _ map[string]any, out any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var v any
	switch system {
	case brain.TriagePrompt:
		f.triageCalls++
		var items []brain.TriageResult
		for _, m := range idRe.FindAllStringSubmatch(user, -1) {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			kind, rel := "news", 8
			if strings.Contains(m[2], "PR:") {
				kind = "pr"
			}
			if strings.Contains(m[2], "boring") {
				rel = 2
			}
			items = append(items, brain.TriageResult{ID: id, Topics: []string{"ai-models"}, Relevance: rel, Kind: kind, TitleVI: m[2]})
		}
		v = brain.TriageResponse{Items: items}
	case brain.SummarizePrompt:
		f.summaryCalls++
		if f.summaryErr != nil {
			return "", f.summaryErr
		}
		v = brain.Summary{HeadlineVI: "Tin tóm tắt", Context: "Bối cảnh <ok> & chi tiết.", KeyPoints: []string{"a", "b", "c"}, WhyItMatters: "Quan trọng"}
	default:
		return "", errors.New("system prompt lạ")
	}
	b, _ := json.Marshal(v)
	return "fake", json.Unmarshal(b, out)
}

func (f *fakeLLM) Embed(_ context.Context, _ string, texts []string, dim int) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.embedTexts += len(texts)
	out := make([][]float32, len(texts))
	for i, tx := range texts {
		hot := 5
		switch {
		case strings.Contains(tx, "event-A"):
			hot = 0
		case strings.Contains(tx, "event-B"):
			hot = 1
		case strings.Contains(tx, "event-C"):
			hot = 2
		}
		out[i] = unit(dim, hot)
	}
	return out, nil
}

func (f *fakeLLM) Remaining(context.Context, string) (int, error) {
	if f.embedBudget > 0 {
		return f.embedBudget - f.embedTexts, nil
	}
	return 1000, nil
}

type sent struct {
	text   string
	markup *telegram.Markup
}

type fakeTG struct {
	mu       sync.Mutex
	msgs     []sent
	failAt   int // gửi thứ N (1-based) trả lỗi một lần; 0 = không
	failOnce bool
}

func (f *fakeTG) SendMessage(_ context.Context, text string, m *telegram.Markup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt > 0 && len(f.msgs)+1 == f.failAt && !f.failOnce {
		f.failOnce = true
		return 0, errors.New("telegram down")
	}
	f.msgs = append(f.msgs, sent{text, m})
	return int64(1000 + len(f.msgs)), nil
}

func newTestPipeline(t *testing.T, sources []config.Source) (*pipeline, *fakeLLM, *fakeTG) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sources = sources
	cfg.Settings.TelegramPacing = 0
	fl, ft := &fakeLLM{}, &fakeTG{}
	p := &pipeline{
		cfg: cfg, feed: http.DefaultClient, llm: fl, tg: ft,
		article: func(context.Context, string) (extract.Result, error) {
			return extract.Result{Text: strings.Repeat("nội dung bài viết ", 50)}, nil
		},
		now:   time.Now,
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	return p, fl, ft
}

func rss(items ...[2]string) string { // {title, link}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>`)
	pub := time.Now().Add(-time.Hour).UTC().Format(time.RFC1123Z)
	for _, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><description>mô tả %s</description><pubDate>%s</pubDate></item>`, it[0], it[1], it[0], pub)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---- tests ----

// AC3 + T1.6: ingest chạy, idempotent, 2 tin cùng sự kiện chung cluster.
func TestIngestIdempotentAndClusters(t *testing.T) {
	resetDB(t)
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rss(
			[2]string{"event-A OpenAI ra mô hình mới", "https://a.example/1?utm_source=x"},
			[2]string{"event-B Rust 2.0 phát hành", "https://a.example/2"},
			[2]string{"PR: công ty X quảng bá", "https://a.example/3"},
			[2]string{"boring tin nhạt", "https://a.example/4"},
		))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rss([2]string{"event-A OpenAI công bố mô hình", "https://b.example/9"}))
	}))
	defer srvB.Close()
	p, fl, _ := newTestPipeline(t, []config.Source{
		{ID: "a", Name: "A", Kind: "rss", URL: srvA.URL, Lang: "en", Section: "ai", Weight: 1},
		{ID: "b", Name: "B", Kind: "rss", URL: srvB.URL, Lang: "vi", Section: "vn", Weight: 1},
	})
	ctx := context.Background()
	st, err := p.runIngest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Inserted != 5 || st.Dropped != 2 || st.Clustered != 3 {
		t.Fatalf("%+v", st)
	}
	if n := count(t, `SELECT count(*) FROM items WHERE state = 'clustered'`); n != 3 {
		t.Fatalf("clustered=%d", n)
	}
	if n := count(t, `SELECT count(*) FROM items WHERE state = 'dropped'`); n != 2 {
		t.Fatalf("dropped=%d", n)
	}
	// event-A của 2 nguồn cùng cluster; event-B tách riêng
	if n := count(t, `SELECT count(DISTINCT cluster_id) FROM items WHERE state = 'clustered'`); n != 2 {
		t.Fatalf("số cluster=%d", n)
	}
	if n := count(t, `SELECT max(n_sources) FROM clusters`); n != 2 {
		t.Fatalf("n_sources=%d", n)
	}
	if n := count(t, `SELECT count(*) FROM clusters WHERE hot_score > 0 AND hot_reason IS NOT NULL`); n != 2 {
		t.Fatalf("hot_score chưa tính: %d", n)
	}
	embedBefore := fl.embedTexts

	// lần 2 ngay sau đó: không item trùng, không embed lại
	st2, err := p.runIngest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Inserted != 0 || fl.embedTexts != embedBefore {
		t.Fatalf("lần 2: %+v embed %d→%d", st2, embedBefore, fl.embedTexts)
	}
	if n := count(t, `SELECT count(*) FROM (SELECT url_canonical FROM items GROUP BY 1 HAVING count(*) > 1) x`); n != 0 {
		t.Fatalf("trùng url: %d", n)
	}
	if n := count(t, `SELECT count(*) FROM items`); n != 5 {
		t.Fatalf("tổng item=%d", n)
	}
}

// T1.2: lỗi 5 lần → tự tắt + cảnh báo Telegram.
func TestSourceAutoDisabledAfter5Failures(t *testing.T) {
	resetDB(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	p, _, tg := newTestPipeline(t, []config.Source{{ID: "bad", Name: "Bad", Kind: "rss", URL: bad.URL, Lang: "en", Section: "tech", Weight: 1}})
	for i := 0; i < 5; i++ {
		if _, err := p.runIngest(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var enabled bool
	var fails int
	if err := db.QueryRow(context.Background(), `SELECT enabled, fail_count FROM sources WHERE id = 'bad'`).Scan(&enabled, &fails); err != nil {
		t.Fatal(err)
	}
	if enabled || fails != 5 {
		t.Fatalf("enabled=%v fails=%d", enabled, fails)
	}
	if len(tg.msgs) != 1 || !strings.Contains(tg.msgs[0].text, "⚠️") {
		t.Fatalf("cảnh báo: %+v", tg.msgs)
	}
	// lượt 6 không fetch nữa, không cảnh báo thêm
	p.runIngest(context.Background())
	if len(tg.msgs) != 1 {
		t.Fatal("không được cảnh báo lặp")
	}
}

// seed: n cluster, rải section/topic để Select có đủ lựa chọn.
func seedClusters(t *testing.T, n int) {
	t.Helper()
	ctx := context.Background()
	secs := []string{"ai", "tech", "vn", "dev"}
	for _, s := range secs {
		if _, err := db.Exec(ctx, `INSERT INTO sources (id, kind, url, lang, section, weight) VALUES ($1,'rss','http://x',$2,$3,1) ON CONFLICT DO NOTHING`,
			"src-"+s, map[bool]string{true: "vi", false: "en"}[s == "vn"], s); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		sec := secs[i%4]
		var cid int64
		if err := db.QueryRow(ctx, `INSERT INTO clusters (first_seen, last_seen, n_sources, section, topics, hot_score)
			VALUES (now(), now(), $1, $2, $3, $4) RETURNING id`, 1+i%4, sec, []string{fmt.Sprintf("topic-%d", i%6)}, float64(100-i)).Scan(&cid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO items (source_id, url_canonical, title, snippet, title_hash, state, cluster_id, published_at)
			VALUES ($1, $2, $3, 'trích đoạn', $4, 'clustered', $5, now())`, "src-"+sec, fmt.Sprintf("https://x.example/%d", i), fmt.Sprintf("Tiêu đề %d", i), fmt.Sprint("h", i), cid); err != nil {
			t.Fatal(err)
		}
	}
}

// AC5 + AC6: đúng 1 header + 20 tin; chạy lại không gửi thêm; resume sau khi cắt giữa chừng.
func TestDigestSendsAndIsIdempotent(t *testing.T) {
	resetDB(t)
	seedClusters(t, 40)
	p, _, tg := newTestPipeline(t, nil)
	res, err := p.runDigest(context.Background(), false)
	if err != nil || !res.Finished || res.Sent != 20 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(tg.msgs) != 21 {
		t.Fatalf("msgs=%d", len(tg.msgs))
	}
	if !strings.Contains(tg.msgs[0].text, "20 tin") || tg.msgs[0].markup != nil {
		t.Fatalf("header: %s", tg.msgs[0].text)
	}
	for i, m := range tg.msgs[1:] {
		if m.markup == nil || m.markup.InlineKeyboard[0][0].Text != "📖 Đọc bài gốc" || !strings.HasPrefix(m.markup.InlineKeyboard[0][0].URL, "https://x.example/") {
			t.Fatalf("msg %d markup: %+v", i, m.markup)
		}
		if !strings.Contains(m.text, "&lt;ok&gt; &amp; chi tiết") {
			t.Fatalf("escape sai: %s", m.text)
		}
	}
	if n := count(t, `SELECT count(*) FROM digest_items WHERE state = 'sent' AND tg_message_id IS NOT NULL`); n != 20 {
		t.Fatalf("sent=%d", n)
	}
	if n := count(t, `SELECT count(*) FROM digests WHERE finished_at IS NOT NULL`); n != 1 {
		t.Fatal("digest phải finished")
	}
	res2, err := p.runDigest(context.Background(), true)
	if err != nil || !res2.AlreadyDone || len(tg.msgs) != 21 {
		t.Fatalf("lần 2: %+v %v msgs=%d", res2, err, len(tg.msgs))
	}
}

func TestDigestResumesAfterFailure(t *testing.T) {
	resetDB(t)
	seedClusters(t, 40)
	p, _, tg := newTestPipeline(t, nil)
	tg.failAt = 9 // header + 7 tin thành công, lần gửi thứ 9 (tin #8) lỗi
	if _, err := p.runDigest(context.Background(), false); err == nil {
		t.Fatal("kỳ vọng lỗi gửi")
	}
	if len(tg.msgs) != 8 || count(t, `SELECT count(*) FROM digest_items WHERE state = 'sent'`) != 7 {
		t.Fatalf("msgs=%d", len(tg.msgs))
	}
	res, err := p.runDigest(context.Background(), false)
	if err != nil || res.Sent != 13 || !res.Finished {
		t.Fatalf("%+v %v", res, err)
	}
	if len(tg.msgs) != 21 {
		t.Fatalf("tổng msgs=%d (header không được gửi lại)", len(tg.msgs))
	}
	if n := count(t, `SELECT count(*) FROM digest_items WHERE state = 'sent'`); n != 20 {
		t.Fatal(n)
	}
	seen := map[string]bool{}
	for _, m := range tg.msgs[1:] {
		if seen[m.text] {
			t.Fatal("gửi trùng một tin")
		}
		seen[m.text] = true
	}
}

// Hết quota: lượt sớm không gửi gì, lượt cuối gửi dạng fallback ⚠️.
func TestDigestFallbackWhenLLMExhausted(t *testing.T) {
	resetDB(t)
	seedClusters(t, 40)
	p, fl, tg := newTestPipeline(t, nil)
	fl.summaryErr = llm.ErrBudgetExhausted
	res, err := p.runDigest(context.Background(), false)
	if err != nil || res.Sent != 0 || len(tg.msgs) != 0 || res.Finished {
		t.Fatalf("%+v %v msgs=%d", res, err, len(tg.msgs))
	}
	if n := count(t, `SELECT count(*) FROM digest_items WHERE state = 'selected'`); n != 20 {
		t.Fatalf("selected=%d", n)
	}
	res, err = p.runDigest(context.Background(), true)
	if err != nil || res.Sent != 20 || !res.Finished {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(tg.msgs[1].text, "⚠️") || !strings.Contains(tg.msgs[1].text, "trích đoạn") {
		t.Fatalf("fallback: %s", tg.msgs[1].text)
	}
	if n := count(t, `SELECT count(*) FROM digest_items WHERE fallback`); n != 20 {
		t.Fatal(n)
	}
}

// Luật gửi lại: chỉ khi n_sources tăng ≥ 3 so với lần gửi trước, kèm nhãn 🔄.
func TestResendRule(t *testing.T) {
	resetDB(t)
	seedClusters(t, 3)
	ctx := context.Background()
	cl := func(n int) int64 { // cluster của item https://x.example/<n>
		return int64(count(t, `SELECT cluster_id FROM items WHERE url_canonical = $1`, fmt.Sprintf("https://x.example/%d", n)))
	}
	c0, c1, c2 := cl(0), cl(1), cl(2)
	db.Exec(ctx, `UPDATE clusters SET last_sent_n_sources = 2, n_sources = 4 WHERE id = $1`, c0) // +2: không đủ
	db.Exec(ctx, `UPDATE clusters SET last_sent_n_sources = 2, n_sources = 5 WHERE id = $1`, c1) // +3: gửi lại
	db.Exec(ctx, `UPDATE clusters SET last_sent_n_sources = NULL WHERE id = $1`, c2)             // chưa gửi
	p, _, tg := newTestPipeline(t, nil)
	if _, err := p.runDigest(ctx, true); err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	rows, _ := db.Query(ctx, `SELECT cluster_id FROM digest_items`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		got[id] = true
	}
	rows.Close()
	if len(got) != 2 || got[c0] || !got[c1] || !got[c2] {
		t.Fatalf("got=%v c0=%d c1=%d c2=%d", got, c0, c1, c2)
	}
	// nhãn 🔄 chỉ khi cluster từng 'sent' ở digest khác
	var old int64
	db.QueryRow(ctx, `INSERT INTO digests (digest_date, finished_at) VALUES ('2000-01-01', now()) RETURNING id`).Scan(&old)
	db.Exec(ctx, `INSERT INTO digest_items (digest_id, cluster_id, rank, state) VALUES ($1, $2, 1, 'sent')`, old, c1)
	db.Exec(ctx, `UPDATE digest_items SET state = 'summarized', summary = '{"headline_vi":"h","context":"c","key_points":["a","b","c"],"why_it_matters":"w"}' WHERE digest_id <> $1`, old)
	tg.msgs = nil
	db.Exec(ctx, `UPDATE digests SET finished_at = NULL, header_sent = true WHERE digest_date <> '2000-01-01'`)
	if _, err := p.runDigest(ctx, true); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range tg.msgs {
		joined += m.text
	}
	if strings.Count(joined, "🔄 Cập nhật") != 1 {
		t.Fatalf("nhãn 🔄: %s", joined)
	}
}

// AC9
func TestRedirect(t *testing.T) {
	resetDB(t)
	seedClusters(t, 1)
	do := func(id, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		redirectCluster(rec, httptest.NewRequest("GET", "/r/"+id+query, nil), id)
		return rec
	}
	for _, id := range []string{"999", "abc", "-1", "0"} {
		if rec := do(id, ""); rec.Code != 404 {
			t.Errorf("id %s → %d", id, rec.Code)
		}
	}
	cid := strconv.Itoa(count(t, `SELECT id FROM clusters LIMIT 1`))
	rec := do(cid, "?url=https://evil.example&redirect=https://evil.example")
	if rec.Code != 302 || rec.Header().Get("Location") != "https://x.example/0" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	if n := count(t, `SELECT count(*) FROM feedback WHERE kind = 'click' AND cluster_id = $1::bigint`, cid); n != 1 {
		t.Fatalf("click=%d", n)
	}
	if n := count(t, `SELECT count(*) FROM feedback`); n != 1 {
		t.Fatal("404 không được ghi click")
	}
}

func TestVNDate(t *testing.T) {
	// 23:30 UTC ngày 14 = 06:30 ICT ngày 15
	if got := vnDate(time.Date(2026, 10, 14, 23, 30, 0, 0, time.UTC)).Format("2006-01-02"); got != "2026-10-15" {
		t.Fatal(got)
	}
	if got := vnDate(time.Date(2026, 10, 14, 16, 59, 0, 0, time.UTC)).Format("2006-01-02"); got != "2026-10-14" {
		t.Fatal(got)
	}
}

func TestDBUsage(t *testing.T) {
	resetDB(t)
	u := dbUsage{}
	ctx := context.Background()
	if n, err := u.Used(ctx, "2026-10-14", "m"); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	u.Add(ctx, "2026-10-14", "m", 3)
	u.Add(ctx, "2026-10-14", "m", 2)
	if n, _ := u.Used(ctx, "2026-10-14", "m"); n != 5 {
		t.Fatal(n)
	}
	if n, _ := u.Used(ctx, "2026-10-15", "m"); n != 0 {
		t.Fatal("ngày khác phải tách riêng")
	}
}

// Aggregator (HN/Lobsters) không tính vào n_sources; section theo chủ đề chứ không theo nguồn.
func TestAggregatorNotCountedAndSectionByTopic(t *testing.T) {
	resetDB(t)
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rss([2]string{"event-A OpenAI ra mô hình", "https://p.example/1"}))
	}))
	defer pub.Close()
	agg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rss([2]string{"event-A OpenAI ra mô hình (thảo luận)", "https://agg.example/x"}))
	}))
	defer agg.Close()
	p, _, _ := newTestPipeline(t, []config.Source{
		{ID: "pub", Name: "Pub", Kind: "rss", URL: pub.URL, Lang: "en", Section: "tech", Weight: 1},
		{ID: "agg", Name: "Agg", Kind: "rss", URL: agg.URL, Lang: "en", Section: "tech", Weight: 1, Aggregator: true},
	})
	if _, err := p.runIngest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM clusters`); n != 1 {
		t.Fatalf("clusters=%d", n)
	}
	if n := count(t, `SELECT n_sources FROM clusters`); n != 1 {
		t.Fatalf("n_sources=%d, aggregator không được tính", n)
	}
	var sec string
	if err := db.QueryRow(context.Background(), `SELECT section FROM clusters`).Scan(&sec); err != nil || sec != "ai" {
		t.Fatalf("section=%q err=%v (nguồn là tech nhưng chủ đề ai-models)", sec, err)
	}
	if n := count(t, `SELECT count(*) FROM items WHERE cluster_id IS NOT NULL`); n != 2 {
		t.Fatal("cả hai item vẫn nằm trong cluster")
	}

	// cluster chỉ có aggregator → tính 1 nguồn
	resetDB(t)
	p2, _, _ := newTestPipeline(t, []config.Source{{ID: "agg", Name: "Agg", Kind: "rss", URL: agg.URL, Lang: "en", Section: "tech", Weight: 1, Aggregator: true}})
	if _, err := p2.runIngest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT n_sources FROM clusters`); n != 1 {
		t.Fatalf("chỉ aggregator: n_sources=%d", n)
	}
}
