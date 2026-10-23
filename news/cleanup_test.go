package news

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// T2.5: xoá bài > retention, giữ cluster đã gửi/có feedback (bỏ embedding), không đụng bài mới.
func TestCleanup(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	p, _, _ := newTestPipeline(t, nil)
	p.cfg.Settings.RetentionDays = 30
	old := time.Now().Add(-40 * 24 * time.Hour)
	vec := vecLiteral(unit(768, 0))

	if _, err := db.Exec(ctx, `INSERT INTO sources (id, kind, url, lang, section, weight) VALUES ('s','rss','http://x','en','ai',1)`); err != nil {
		t.Fatal(err)
	}
	cluster := func(seen time.Time) int64 {
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO clusters (first_seen, last_seen, centroid, topics, section, n_sources) VALUES ($1,$1,$2::halfvec,'{}','ai',1) RETURNING id`, seen, vec).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	item := func(name string, fetched time.Time, cid *int64, state string) {
		if _, err := db.Exec(ctx, `INSERT INTO items (source_id, url_canonical, title, title_hash, state, embedding, cluster_id, fetched_at)
			VALUES ('s',$1,$1,$1,$2,$3::halfvec,$4,$5)`, "https://x/"+name, state, vec, cid, fetched); err != nil {
			t.Fatal(err)
		}
	}
	has := func(name string) bool {
		return count(t, `SELECT count(*) FROM items WHERE url_canonical = $1`, "https://x/"+name) == 1
	}

	plain, sent, rated, fresh := cluster(old), cluster(old), cluster(old), cluster(time.Now())
	item("dropped", old, nil, "dropped")
	item("plain", old, &plain, "clustered")
	item("sent", old, &sent, "clustered")
	item("rated", old, &rated, "clustered")
	item("fresh-in-old-cluster-nope", time.Now(), &fresh, "clustered")
	item("fresh", time.Now(), &fresh, "clustered")
	var did int64
	if err := db.QueryRow(ctx, `INSERT INTO digests (digest_date) VALUES ('2026-09-01') RETURNING id`).Scan(&did); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO digest_items (digest_id, cluster_id, rank, state) VALUES ($1,$2,1,'sent')`, did, sent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO feedback (cluster_id, kind) VALUES ($1,'save')`, rated); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"2000-01-01", time.Now().Format("2006-01-02")} {
		if _, err := db.Exec(ctx, `INSERT INTO llm_usage (day, model, requests) VALUES ($1,'m',1)`, d); err != nil {
			t.Fatal(err)
		}
	}

	st, err := p.runCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"dropped": false, "plain": false, "sent": true, "rated": true, "fresh": true} {
		if has(name) != want {
			t.Errorf("item %s: còn=%v, want %v", name, has(name), want)
		}
	}
	if st.ItemsDeleted != 2 || st.ClustersDeleted != 1 || st.UsageDeleted != 1 || st.DBSizeBytes <= 0 {
		t.Fatalf("%+v", st)
	}
	if count(t, `SELECT count(*) FROM clusters WHERE id = $1`, plain) != 0 || count(t, `SELECT count(*) FROM clusters WHERE id IN ($1,$2,$3)`, sent, rated, fresh) != 3 {
		t.Fatal("cluster chưa gửi/chưa chấm phải bị xoá; cluster đã gửi/chấm/mới phải còn")
	}
	// bài giữ lại mất embedding; cluster cũ mất centroid; cái mới còn nguyên
	if count(t, `SELECT count(*) FROM items WHERE url_canonical IN ('https://x/sent','https://x/rated') AND embedding IS NULL`) != 2 ||
		count(t, `SELECT count(*) FROM items WHERE url_canonical = 'https://x/fresh' AND embedding IS NOT NULL`) != 1 {
		t.Fatal("embedding")
	}
	if count(t, `SELECT count(*) FROM clusters WHERE id IN ($1,$2) AND centroid IS NULL`, sent, rated) != 2 ||
		count(t, `SELECT count(*) FROM clusters WHERE id = $1 AND centroid IS NOT NULL`, fresh) != 1 {
		t.Fatal("centroid")
	}
	// /r/:id vẫn chạy cho cluster đã gửi sau cleanup
	if n := count(t, `SELECT count(*) FROM items WHERE cluster_id = $1`, sent); n != 1 {
		t.Fatal(fmt.Sprint("redirect cần item: ", n))
	}
	// chạy lại: không còn gì để xoá
	if st, err = p.runCleanup(ctx); err != nil || st.ItemsDeleted != 0 || st.ClustersDeleted != 0 || st.EmbeddingsShrunk != 0 {
		t.Fatalf("idempotent: %+v %v", st, err)
	}
}
