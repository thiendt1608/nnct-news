package news

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"encore.app/news/config"
)

const (
	testSecret = "s3cret-token"
	testChat   = int64(4242)
)

func botPipeline(t *testing.T) (*pipeline, *fakeTG) {
	t.Helper()
	resetDB(t)
	p, _, tg := newTestPipeline(t, nil)
	p.webhookSecret, p.chatID = testSecret, fmt.Sprint(testChat)
	p.cfg.Sources = []config.Source{{ID: "src-ai", Name: "AI Src"}, {ID: "agg", Name: "Agg", Aggregator: true}}
	return p, tg
}

// seedFeedbackCluster tạo 1 cluster 2 topic với 1 bài từ nguồn thường và 1 bài từ aggregator.
func seedFeedbackCluster(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"src-ai", "agg"} {
		if _, err := db.Exec(ctx, `INSERT INTO sources (id, kind, url, lang, section, weight) VALUES ($1,'rss','http://x','en','ai',1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	var cid int64
	if err := db.QueryRow(ctx, `INSERT INTO clusters (topics, section, n_sources) VALUES ($1, 'ai', 1) RETURNING id`,
		[]string{"ai-models", "security"}).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	for i, sid := range []string{"src-ai", "agg"} {
		if _, err := db.Exec(ctx, `INSERT INTO items (source_id, url_canonical, title, title_hash, state, cluster_id, published_at)
			VALUES ($1, $2, $3, $4, 'clustered', $5, now())`, sid, fmt.Sprintf("https://x.example/fb/%d?a=1&b=2", i), fmt.Sprintf("Bài <%d> & co", i), fmt.Sprint("fh", i), cid); err != nil {
			t.Fatal(err)
		}
	}
	return cid
}

func callbackUpdate(chat int64, data string) []byte {
	b, _ := json.Marshal(map[string]any{"update_id": 1, "callback_query": map[string]any{
		"id": "cb1", "data": data, "message": map[string]any{"message_id": 77, "chat": map[string]any{"id": chat}}}})
	return b
}

func post(p *pipeline, secret string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(string(body)))
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	rec := httptest.NewRecorder()
	p.serveWebhook(rec, req)
	return rec
}

func topicAdj(t *testing.T) map[string]float64 {
	t.Helper()
	var raw string
	if err := db.QueryRow(context.Background(), `SELECT value::text FROM profile WHERE key = 'topic_adj'`).Scan(&raw); err != nil {
		if isNoRows(err) {
			return map[string]float64{}
		}
		t.Fatal(err)
	}
	m := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func near(a, b float64) bool { d := a - b; return d < 1e-6 && d > -1e-6 }

func sourceAdj(t *testing.T, id string) float64 {
	t.Helper()
	var v float64
	if err := db.QueryRow(context.Background(), `SELECT weight_adj FROM sources WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// AC13: thiếu/sai secret → 401; chat lạ → bỏ qua (200, không ghi gì).
func TestWebhookAuthAndAllowlist(t *testing.T) {
	p, tg := botPipeline(t)
	cid := seedFeedbackCluster(t)
	body := callbackUpdate(testChat, fmt.Sprintf("up:%d", cid))

	for name, secret := range map[string]string{"thiếu header": "", "sai secret": "nope", "gần đúng": testSecret + "x"} {
		if rec := post(p, secret, body); rec.Code != 401 {
			t.Errorf("%s → %d", name, rec.Code)
		}
	}
	p.webhookSecret = ""
	if rec := post(p, "", body); rec.Code != 401 {
		t.Errorf("secret server rỗng phải từ chối tất cả → %d", rec.Code)
	}
	p.webhookSecret = testSecret
	if n := count(t, `SELECT count(*) FROM feedback`); n != 0 || len(tg.answers) != 0 {
		t.Fatal("request không xác thực không được có tác dụng")
	}

	if rec := post(p, testSecret, callbackUpdate(testChat+1, fmt.Sprintf("up:%d", cid))); rec.Code != 200 {
		t.Fatalf("chat lạ → %d", rec.Code)
	}
	if n := count(t, `SELECT count(*) FROM feedback`); n != 0 || len(tg.answers) != 0 || len(tg.edits) != 0 {
		t.Fatal("update từ chat lạ phải bị bỏ qua")
	}

	if rec := post(p, testSecret, []byte("{bad")); rec.Code != 400 {
		t.Fatalf("JSON hỏng → %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, webhookPath, nil)
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testSecret)
	rec := httptest.NewRecorder()
	p.serveWebhook(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET → %d", rec.Code)
	}
}

// AC14: 👍 👎 🔖 → ghi feedback, đổi nút ✅, đổi trọng số đúng +0.05 / −0.1; bấm lại không cộng dồn.
func TestFeedbackCallbacksLearn(t *testing.T) {
	p, tg := botPipeline(t)
	cid := seedFeedbackCluster(t)

	press := func(kind string) {
		t.Helper()
		if rec := post(p, testSecret, callbackUpdate(testChat, fmt.Sprintf("%s:%d", kind, cid))); rec.Code != 200 {
			t.Fatalf("%s → %d", kind, rec.Code)
		}
	}
	press("up")
	if a := topicAdj(t); !near(a["ai-models"], 0.05) || !near(a["security"], 0.05) {
		t.Fatalf("sau 👍: %v", a)
	}
	if !near(sourceAdj(t, "src-ai"), 0.02) || sourceAdj(t, "agg") != 0 {
		t.Fatalf("source: src-ai=%v agg=%v (aggregator không được học)", sourceAdj(t, "src-ai"), sourceAdj(t, "agg"))
	}
	if last := tg.edits[len(tg.edits)-1]; last.msgID != 77 || last.markup.InlineKeyboard[1][0].Text != "✅ 👍" || last.markup.InlineKeyboard[1][1].Text != "👎" {
		t.Fatalf("nút: %+v", last.markup.InlineKeyboard)
	}
	if got := tg.answers[len(tg.answers)-1]; got != "cb1|👍 Đã ghi nhận" {
		t.Fatal(got)
	}

	press("up") // bấm lại
	if a := topicAdj(t); !near(a["ai-models"], 0.05) || !near(sourceAdj(t, "src-ai"), 0.02) {
		t.Fatalf("bấm lại không được cộng dồn: %v", a)
	}
	if got := tg.answers[len(tg.answers)-1]; got != "cb1|Đã ghi nhận trước đó" {
		t.Fatal(got)
	}
	press("down") // 👍 đã có: 👎 đến sau không được tính (mỗi tin một phiếu)
	if n := count(t, `SELECT count(*) FROM feedback WHERE kind IN ('up','down')`); n != 1 {
		t.Fatalf("votes=%d", n)
	}

	press("save")
	if a := topicAdj(t); !near(a["ai-models"], 0.10) {
		t.Fatalf("🔖 cũng cộng: %v", a)
	}
	last := tg.edits[len(tg.edits)-1].markup.InlineKeyboard[1]
	if last[0].Text != "✅ 👍" || last[2].Text != "✅ Đã lưu" {
		t.Fatalf("%+v", last)
	}
	if n := count(t, `SELECT count(*) FROM feedback WHERE cluster_id = $1 AND kind IN ('up','save')`, cid); n != 2 {
		t.Fatalf("feedback rows=%d", n)
	}
}

func TestFeedbackDownAndClamp(t *testing.T) {
	p, _ := botPipeline(t)
	cid := seedFeedbackCluster(t)
	ctx := context.Background()
	if applied, err := p.recordFeedback(ctx, cid, "down"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if a := topicAdj(t); !near(a["ai-models"], -0.1) || !near(sourceAdj(t, "src-ai"), -0.04) {
		t.Fatalf("👎 trừ gấp đôi: %v %v", a, sourceAdj(t, "src-ai"))
	}
	// kẹp: ép weight_adj nguồn về sát sàn rồi 👎 thêm — weight + weight_adj không dưới 0.2
	if _, err := db.Exec(ctx, `UPDATE sources SET weight_adj = -0.79 WHERE id = 'src-ai'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM feedback`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.recordFeedback(ctx, cid, "down"); err != nil {
		t.Fatal(err)
	}
	if !near(sourceAdj(t, "src-ai"), -0.8) { // weight 1 → sàn 0.2
		t.Fatalf("sàn nguồn: %v", sourceAdj(t, "src-ai"))
	}
	if _, err := p.recordFeedback(ctx, 999999, "up"); err != errClusterNotFound {
		t.Fatalf("cluster lạ: %v", err)
	}
	if _, err := p.recordFeedback(ctx, cid, "click"); err == nil {
		t.Fatal("kind không học phải lỗi")
	}
}

// Trọng số học được phải đi vào hot score ở lần refresh kế tiếp.
func TestLearnedWeightsAffectScore(t *testing.T) {
	p, _ := botPipeline(t)
	cid := seedFeedbackCluster(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE items SET triage = '{"topics":["ai-models"],"relevance":8}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	hot := func() float64 {
		t.Helper()
		if err := p.refreshClusters(ctx, p.now().Add(-1000*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var h float64
		if err := db.QueryRow(ctx, `SELECT hot_score FROM clusters WHERE id = $1`, cid).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	before := hot()
	for i := 0; i < 3; i++ { // 3 tin khác nhau cùng topic bị 👎: gộp thành adj topic
		if _, err := db.Exec(ctx, `INSERT INTO clusters (topics, section, n_sources) VALUES ('{ai-models}', 'ai', 1)`); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := db.Query(ctx, `SELECT id FROM clusters WHERE id <> $1`, cid)
	var others []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		others = append(others, id)
	}
	rows.Close()
	for _, id := range others {
		if _, err := p.recordFeedback(ctx, id, "down"); err != nil {
			t.Fatal(err)
		}
	}
	if after := hot(); !(after < before) {
		t.Fatalf("topic bị 👎 phải nóng ít hơn: %v → %v", before, after)
	}
}

func TestOtherSourcesAndInvalidCallback(t *testing.T) {
	p, tg := botPipeline(t)
	cid := seedFeedbackCluster(t)
	post(p, testSecret, callbackUpdate(testChat, fmt.Sprintf("src:%d", cid)))
	if len(tg.msgs) != 1 {
		t.Fatalf("msgs=%d", len(tg.msgs))
	}
	m := tg.msgs[0].text
	if !strings.Contains(m, "AI Src") || !strings.Contains(m, "Agg") ||
		!strings.Contains(m, `href="https://x.example/fb/0?a=1&amp;b=2"`) || !strings.Contains(m, "Bài &lt;0&gt; &amp; co") {
		t.Fatalf("HTML phải được escape: %s", m)
	}
	if n := count(t, `SELECT count(*) FROM feedback`); n != 0 {
		t.Fatal("📚 không ghi feedback")
	}
	post(p, testSecret, callbackUpdate(testChat, "rm -rf:1"))
	post(p, testSecret, callbackUpdate(testChat, "up:999999"))
	if got := tg.answers[len(tg.answers)-2:]; got[0] != "cb1|Nút không hợp lệ" || got[1] != "cb1|Tin này không còn trong dữ liệu" {
		t.Fatalf("%v", got)
	}
}

func TestEnsureWebhookOnce(t *testing.T) {
	p, tg := botPipeline(t)
	p.ensureWebhook(context.Background())
	if len(tg.webhooks) != 0 {
		t.Fatal("publicBase rỗng (local) thì không đăng ký")
	}
	p.publicBase = "https://staging-x.encr.app/"
	p.ensureWebhook(context.Background())
	p.ensureWebhook(context.Background())
	if len(tg.webhooks) != 1 || tg.webhooks[0] != "https://staging-x.encr.app/telegram/webhook|"+testSecret {
		t.Fatalf("%v", tg.webhooks)
	}
}
