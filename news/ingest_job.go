package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
	"golang.org/x/sync/errgroup"

	"encore.app/news/brain"
	"encore.app/news/config"
	"encore.app/news/ingest"
	"encore.app/news/llm"
	"encore.app/news/telegram"
)

const maxSourceFailures = 5

// IngestStats summarizes one ingest run (logged; Encore cron endpoints return only error).
type IngestStats struct {
	Fetched, Inserted, Triaged, Dropped, Embedded, Clustered int
	FailedSources                                            []string
	LLMStopped                                               string
}

// runIngest: fetch → dedupe → triage → embed → cluster → score. Idempotent; leftovers
// (budget/quota) stay in their state for the next run.
func (p *pipeline) runIngest(ctx context.Context) (IngestStats, error) {
	var st IngestStats
	if !p.runMu.TryLock() {
		return st, errors.New("ingest/digest đang chạy")
	}
	defer p.runMu.Unlock()
	return p.ingestLocked(ctx)
}

func (p *pipeline) ingestLocked(ctx context.Context) (IngestStats, error) {
	var st IngestStats
	if err := p.syncSources(ctx); err != nil {
		return st, fmt.Errorf("sync sources: %w", err)
	}
	if err := p.fetchAll(ctx, &st); err != nil {
		return st, fmt.Errorf("fetch: %w", err)
	}
	if err := p.triage(ctx, &st); err != nil {
		return st, fmt.Errorf("triage: %w", err)
	}
	if err := p.embed(ctx, &st); err != nil {
		return st, fmt.Errorf("embed: %w", err)
	}
	if err := p.cluster(ctx, &st); err != nil {
		return st, fmt.Errorf("cluster: %w", err)
	}
	rlog.Info("ingest xong", "fetched", st.Fetched, "inserted", st.Inserted, "triaged", st.Triaged,
		"dropped", st.Dropped, "embedded", st.Embedded, "clustered", st.Clustered, "failed_sources", st.FailedSources, "llm_stopped", st.LLMStopped)
	return st, nil
}

func (p *pipeline) syncSources(ctx context.Context) error {
	for _, s := range p.cfg.Sources {
		_, err := db.Exec(ctx, `
			INSERT INTO sources (id, kind, url, lang, section, weight, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET kind = EXCLUDED.kind, url = EXCLUDED.url,
				lang = EXCLUDED.lang, section = EXCLUDED.section,
				enabled = sources.enabled AND EXCLUDED.enabled`,
			s.ID, s.Kind, s.URL, s.Lang, s.Section, s.Weight, !s.Disabled)
		if err != nil {
			return err
		}
	}
	return nil
}

type fetchOutcome struct {
	src config.Source
	res ingest.Result
	err error
}

func (p *pipeline) fetchAll(ctx context.Context, st *IngestStats) error {
	rows, err := db.Query(ctx, `SELECT id, COALESCE(etag,''), COALESCE(last_modified,'') FROM sources WHERE enabled`)
	if err != nil {
		return err
	}
	type fs struct{ etag, lm string }
	state := map[string]fs{}
	for rows.Next() {
		var id, e, l string
		if err := rows.Scan(&id, &e, &l); err != nil {
			rows.Close()
			return err
		}
		state[id] = fs{e, l}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var mu sync.Mutex
	var outs []fetchOutcome
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, s := range p.cfg.Sources {
		cur, ok := state[s.ID]
		if !ok {
			continue // disabled trong DB
		}
		g.Go(func() error {
			res, err := ingest.Fetch(gctx, p.feed, s, ingest.FetchState{ETag: cur.etag, LastModified: cur.lm})
			mu.Lock()
			outs = append(outs, fetchOutcome{s, res, err})
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	now := p.now()
	maxAge := time.Duration(p.cfg.Settings.MaxItemAgeHrs) * time.Hour
	for _, o := range outs {
		if o.err != nil {
			st.FailedSources = append(st.FailedSources, o.src.ID)
			if err := p.recordFailure(ctx, o.src, o.err); err != nil {
				return err
			}
			continue
		}
		if _, err := db.Exec(ctx, `UPDATE sources SET etag = NULLIF($2,''), last_modified = NULLIF($3,''),
			fail_count = 0, last_fetched_at = $4 WHERE id = $1`,
			o.src.ID, o.res.State.ETag, o.res.State.LastModified, now); err != nil {
			return err
		}
		st.Fetched += len(o.res.Items)
		for _, it := range ingest.Dedupe(o.res.Items) {
			if !ingest.Fresh(it, now, maxAge) {
				continue
			}
			n, err := p.insertItem(ctx, it, now)
			if err != nil {
				return err
			}
			st.Inserted += n
		}
	}
	return nil
}

func (p *pipeline) recordFailure(ctx context.Context, src config.Source, ferr error) error {
	var count int
	if err := db.QueryRow(ctx, `UPDATE sources SET fail_count = fail_count + 1 WHERE id = $1 RETURNING fail_count`, src.ID).Scan(&count); err != nil {
		return err
	}
	rlog.Warn("nguồn lỗi", "source", src.ID, "fail_count", count, "err", ferr.Error())
	if count >= maxSourceFailures {
		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = false WHERE id = $1`, src.ID); err != nil {
			return err
		}
		p.alert(ctx, fmt.Sprintf("Nguồn %s lỗi %d lần liên tiếp nên đã tắt. Lỗi cuối: %s", src.Name, count, ferr.Error()))
	}
	return nil
}

// alert sends a best-effort ⚠️ message to Telegram.
func (p *pipeline) alert(ctx context.Context, text string) {
	if _, err := p.tg.SendMessage(ctx, "⚠️ "+telegram.Escape(text), nil); err != nil {
		rlog.Warn("không gửi được cảnh báo", "err", err.Error())
	}
}

// insertItem inserts one item. Title-hash dedupe is limited to the same source within 48h
// (khác DESIGN §5.2: cùng tiêu đề ở nguồn khác là một nguồn nữa cho cluster, không phải trùng).
// A repeated URL only refreshes engagement when points grew (HN points tăng dần).
func (p *pipeline) insertItem(ctx context.Context, it ingest.Item, now time.Time) (int, error) {
	pub := it.PublishedAt
	if pub.IsZero() {
		pub = now
	}
	eng := "{}"
	if len(it.Engagement) > 0 {
		b, _ := json.Marshal(it.Engagement)
		eng = string(b)
	}
	var inserted bool
	err := db.QueryRow(ctx, `
		INSERT INTO items (source_id, url_canonical, title, snippet, lang, published_at, fetched_at, engagement, title_hash)
		SELECT $1, $2, $3, $4, $5, $6, $9, $7::jsonb, $8
		WHERE NOT EXISTS (SELECT 1 FROM items WHERE title_hash = $8 AND source_id = $1 AND fetched_at > $9::timestamptz - interval '48 hours')
		ON CONFLICT (url_canonical) DO UPDATE SET engagement = EXCLUDED.engagement
		WHERE COALESCE((EXCLUDED.engagement->>'points')::int, 0) > COALESCE((items.engagement->>'points')::int, 0)
		RETURNING (xmax = 0)`,
		it.SourceID, it.URLCanonical, it.Title, it.Snippet, it.Lang, pub, eng, it.TitleHash, now).Scan(&inserted)
	if errors.Is(err, sqldb.ErrNoRows) {
		return 0, nil // trùng, không có gì đổi
	}
	if err != nil {
		return 0, err
	}
	if inserted {
		return 1, nil
	}
	return 0, nil
}

// transient reports whether an LLM error is worth retrying later without burning item attempts.
func transient(err error) bool {
	if errors.Is(err, llm.ErrBadJSON) {
		return false
	}
	var he *llm.HTTPError
	if errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != 429 {
		return false
	}
	return true
}

func (p *pipeline) bumpAttempts(ctx context.Context, ids []int64) error {
	_, err := db.Exec(ctx, `UPDATE items SET attempts = attempts + 1,
		state = CASE WHEN attempts + 1 >= 5 THEN 'dropped' ELSE state END WHERE id = ANY($1)`, ids)
	return err
}

func (p *pipeline) triage(ctx context.Context, st *IngestStats) error {
	s := p.cfg.Settings
	muted, _, err := p.keywords(ctx)
	if err != nil {
		return err
	}
	for b := 0; b < s.TriageMaxBatches; b++ {
		rows, err := db.Query(ctx, `SELECT id, title, COALESCE(snippet,''), source_id FROM items
			WHERE state = 'new' AND attempts < 5 ORDER BY published_at DESC, id DESC LIMIT $1`, s.TriageBatch)
		if err != nil {
			return err
		}
		var batch []brain.TriageInput
		titles := map[int64]string{}
		for rows.Next() {
			var in brain.TriageInput
			var src string
			if err := rows.Scan(&in.ID, &in.Title, &in.Snippet, &src); err != nil {
				rows.Close()
				return err
			}
			in.Source = p.sourceName(src)
			batch = append(batch, in)
			titles[in.ID] = in.Title
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		var resp brain.TriageResponse
		_, err = p.llm.GenerateJSON(ctx, s.TriageModels, brain.TriagePrompt, brain.BuildTriageUser(batch), brain.TriageSchema(), &resp)
		ids := make([]int64, len(batch))
		for i, in := range batch {
			ids[i] = in.ID
		}
		if err != nil {
			if errors.Is(err, llm.ErrBudgetExhausted) || transient(err) {
				st.LLMStopped = "triage: " + err.Error()
				return nil
			}
			if err := p.bumpAttempts(ctx, ids); err != nil {
				return err
			}
			continue
		}
		results := brain.ValidateTriage(resp, batch)
		var missing []int64
		for _, in := range batch {
			r, ok := results[in.ID]
			if !ok {
				missing = append(missing, in.ID)
				continue
			}
			keep, reason := brain.TriageVerdict(r, in.Title, s.MinRelevance, muted)
			state := "triaged"
			if !keep {
				state = "dropped"
				st.Dropped++
			} else {
				st.Triaged++
			}
			tj := brain.TriageJSON(r)
			if _, err := db.Exec(ctx, `UPDATE items SET state = $2, triage = $3::jsonb WHERE id = $1`, in.ID, state, string(tj)); err != nil {
				return err
			}
			_ = reason
		}
		if len(missing) > 0 {
			if err := p.bumpAttempts(ctx, missing); err != nil {
				return err
			}
		}
	}
	return nil
}

func embedText(title, snippet string) string {
	r := []rune(snippet)
	if len(r) > 300 {
		r = r[:300]
	}
	return strings.TrimSpace(title + " — " + string(r))
}

func (p *pipeline) embed(ctx context.Context, st *IngestStats) error {
	s := p.cfg.Settings
	for b := 0; b < s.EmbedMaxBatches; b++ {
		rem, err := p.llm.Remaining(ctx, s.EmbeddingModel)
		if err != nil {
			return err
		}
		n := min(s.EmbedBatch, rem)
		if n <= 0 {
			st.LLMStopped = "embed: hết budget"
			return nil
		}
		rows, err := db.Query(ctx, `SELECT id, title, COALESCE(snippet,'') FROM items
			WHERE state = 'triaged' AND attempts < 5 ORDER BY published_at DESC, id DESC LIMIT $1`, n)
		if err != nil {
			return err
		}
		var ids []int64
		var texts []string
		for rows.Next() {
			var id int64
			var t, sn string
			if err := rows.Scan(&id, &t, &sn); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			texts = append(texts, embedText(t, sn))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		vecs, err := p.llm.Embed(ctx, s.EmbeddingModel, texts, s.EmbeddingDim)
		if err != nil {
			if errors.Is(err, llm.ErrBudgetExhausted) || transient(err) {
				st.LLMStopped = "embed: " + err.Error()
				return nil
			}
			if err := p.bumpAttempts(ctx, ids); err != nil {
				return err
			}
			continue
		}
		for i, id := range ids {
			if _, err := db.Exec(ctx, `UPDATE items SET embedding = $2::halfvec, state = 'embedded' WHERE id = $1`, id, vecLiteral(vecs[i])); err != nil {
				return err
			}
			st.Embedded++
		}
	}
	return nil
}
