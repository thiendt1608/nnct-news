package news

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"encore.app/news/telegram"
)

func TestParseCommand(t *testing.T) {
	for in, want := range map[string][2]string{
		"/today":              {"today", ""},
		"/More@nnct_bot ai 2": {"more", "ai 2"},
		"  /mute  quảng cáo ": {"mute", "quảng cáo"},
	} {
		c, a, ok := telegram.ParseCommand(in)
		if !ok || c != want[0] || a != want[1] {
			t.Errorf("%q → %q %q %v", in, c, a, ok)
		}
	}
	for _, in := range []string{"", "hello", "/", "/@bot", "xin /today"} {
		if _, _, ok := telegram.ParseCommand(in); ok {
			t.Errorf("%q không phải lệnh", in)
		}
	}
}

func cmdUpdate(chat int64, text string) []byte {
	b, _ := json.Marshal(map[string]any{"update_id": 2, "message": map[string]any{"message_id": 9, "chat": map[string]any{"id": chat}, "text": text}})
	return b
}

// run sends a command through the webhook and returns the reply text.
func runCmd(t *testing.T, p *pipeline, tg *fakeTG, text string) string {
	t.Helper()
	before := len(tg.msgs)
	if rec := post(p, testSecret, cmdUpdate(testChat, text)); rec.Code != 200 {
		t.Fatalf("%s: %d", text, rec.Code)
	}
	if len(tg.msgs) != before+1 {
		t.Fatalf("%s: kỳ vọng đúng 1 trả lời, có %d", text, len(tg.msgs)-before)
	}
	return tg.msgs[len(tg.msgs)-1].text
}

// T2.3: /today /more /saved /search /mute /boost /status.
func TestBotCommands(t *testing.T) {
	p, tg := botPipeline(t)
	ctx := context.Background()
	seedClusters(t, 40)
	if _, err := p.runDigest(ctx, false); err != nil {
		t.Fatal(err)
	}

	// /today: 20 tin của digest
	out := runCmd(t, p, tg, "/today")
	if n := strings.Count(out, "<a href="); n != 20 || !strings.Contains(out, "Digest") {
		t.Fatalf("today: %d link\n%s", n, out)
	}

	// /more: 5 tin chưa gửi, trang 2 khác trang 1, lọc theo section, không trùng tin đã gửi
	p1 := runCmd(t, p, tg, "/more")
	p2 := runCmd(t, p, tg, "/more 2")
	if strings.Count(p1, "<a href=") != 5 || strings.Count(p2, "<a href=") != 5 || p1 == p2 {
		t.Fatalf("more\n%s\n%s", p1, p2)
	}
	if out := runCmd(t, p, tg, "/more vn"); !strings.Contains(out, "🇻🇳") || strings.Contains(out, "🤖") {
		t.Fatalf("more vn: %s", out)
	}
	if out := runCmd(t, p, tg, "/more zzz"); !strings.Contains(out, "Hết tin") {
		t.Fatalf("more zzz: %s", out)
	}

	// /saved: trống rồi có tin sau khi 🔖
	if out := runCmd(t, p, tg, "/saved"); !strings.Contains(out, "Chưa lưu") {
		t.Fatal(out)
	}
	var cid int64
	if err := db.QueryRow(ctx, `SELECT cluster_id FROM digest_items WHERE rank = 1`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if _, err := p.recordFeedback(ctx, cid, "save"); err != nil {
		t.Fatal(err)
	}
	if out := runCmd(t, p, tg, "/saved"); strings.Count(out, "<a href=") != 1 {
		t.Fatal(out)
	}

	// /search: khớp tiêu đề, escape ký tự LIKE
	if out := runCmd(t, p, tg, "/search Tiêu đề 7"); !strings.Contains(out, "Tiêu đề 7") {
		t.Fatal(out)
	}
	if out := runCmd(t, p, tg, "/search %"); !strings.Contains(out, "Không thấy") {
		t.Fatalf("%% phải là ký tự thường: %s", out)
	}
	if out := runCmd(t, p, tg, "/search"); !strings.Contains(out, "Dùng") {
		t.Fatal(out)
	}

	// /mute /boost: lưu vào profile, gộp với profile.yaml, bỏ được, không trùng
	runCmd(t, p, tg, "/mute  Rao  Vặt ")
	runCmd(t, p, tg, "/mute rao vặt")
	runCmd(t, p, tg, "/boost kubernetes")
	muted, boosted, err := p.keywords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := countOf(muted, "rao vặt"); n != 1 || countOf(muted, "crypto airdrop") != 1 {
		t.Fatalf("muted %v", muted)
	}
	if countOf(boosted, "kubernetes") != 1 || countOf(boosted, "Claude") != 1 {
		t.Fatalf("boosted %v", boosted)
	}
	runCmd(t, p, tg, "/unmute rao vặt")
	if muted, _, _ = p.keywords(ctx); countOf(muted, "rao vặt") != 0 {
		t.Fatalf("unmute: %v", muted)
	}
	if out := runCmd(t, p, tg, "/mute"); !strings.Contains(out, "Dùng: /mute") {
		t.Fatal(out)
	}
	if out := runCmd(t, p, tg, "/mute "+strings.Repeat("x", 61)); !strings.Contains(out, "dài quá") {
		t.Fatal(out)
	}

	// /status + help
	out = runCmd(t, p, tg, "/status")
	if !strings.Contains(out, "MB") || !strings.Contains(out, "Gemini") {
		t.Fatal(out)
	}
	if out := runCmd(t, p, tg, "/help"); !strings.Contains(out, "/today") {
		t.Fatal(out)
	}

	// chat lạ gọi lệnh: bị bỏ qua, không trả lời
	n := len(tg.msgs)
	post(p, testSecret, cmdUpdate(999, "/status"))
	if len(tg.msgs) != n {
		t.Fatal("chat lạ không được nhận trả lời")
	}
}

func countOf(l []string, s string) int {
	n := 0
	for _, x := range l {
		if x == s {
			n++
		}
	}
	return n
}
