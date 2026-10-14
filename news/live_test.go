package news

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveSmoke chạy pipeline thật (feed thật + Gemini + Telegram) bằng secrets local.
// Bỏ qua mặc định. Chạy: NNCT_LIVE=1 encore test -run TestLiveSmoke -v -timeout 30m ./news/
// Cảnh báo: tốn quota Gemini và gửi tin thật vào chat của bot.
func TestLiveSmoke(t *testing.T) {
	if os.Getenv("NNCT_LIVE") != "1" {
		t.Skip("set NNCT_LIVE=1 để chạy")
	}
	resetDB(t)
	p, err := newPipeline()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	for i := 0; i < 3; i++ {
		st, err := p.runIngest(ctx)
		t.Logf("ingest #%d: %+v err=%v", i+1, st, err)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := db.Query(ctx, `SELECT state, count(*) FROM items GROUP BY 1 ORDER BY 1`)
	for rows.Next() {
		var s string
		var n int
		rows.Scan(&s, &n)
		t.Logf("items %s = %d", s, n)
	}
	rows.Close()
	res, err := p.runDigest(ctx, true)
	t.Logf("digest: %+v err=%v", res, err)
	if err != nil {
		t.Fatal(err)
	}
}
