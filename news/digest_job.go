package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"encore.dev/rlog"

	"encore.app/news/brain"
	"encore.app/news/llm"
	"encore.app/news/telegram"
)

// DigestResult describes what one digest run did (for logs/tests).
type DigestResult struct {
	Date        string
	AlreadyDone bool
	Selected    int
	Summarized  int
	Sent        int
	Finished    bool
}

// runDigest builds (once per VN day) and sends the digest. final=true is the late run:
// items that still cannot be summarized go out as title+snippet fallbacks.
func (p *pipeline) runDigest(ctx context.Context, final bool) (DigestResult, error) {
	var res DigestResult
	if !p.runMu.TryLock() {
		return res, errors.New("ingest/digest đang chạy")
	}
	defer p.runMu.Unlock()

	day := vnDate(p.now()).Format("2006-01-02")
	res.Date = day
	var digestID int64
	var finished bool
	var headerSent bool
	err := db.QueryRow(ctx, `
		INSERT INTO digests (digest_date) VALUES ($1::date)
		ON CONFLICT (digest_date) DO UPDATE SET digest_date = EXCLUDED.digest_date
		RETURNING id, finished_at IS NOT NULL, header_sent`, day).Scan(&digestID, &finished, &headerSent)
	if err != nil {
		return res, err
	}
	if finished {
		res.AlreadyDone = true
		return res, nil
	}

	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM digest_items WHERE digest_id = $1`, digestID).Scan(&n); err != nil {
		return res, err
	}
	if n == 0 {
		// lượt ingest cuối để có tin mới nhất; lỗi ở đây không chặn digest
		if _, err := p.ingestLocked(ctx); err != nil {
			rlog.Warn("ingest trước digest lỗi", "err", err.Error())
		}
		if n, err = p.selectDigest(ctx, digestID); err != nil {
			return res, err
		}
	}
	res.Selected = n
	if n == 0 {
		return res, nil
	}

	if err := p.summarizePending(ctx, digestID, final, &res); err != nil {
		return res, err
	}
	if err := p.sendPending(ctx, digestID, &headerSent, &res); err != nil {
		return res, err
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM digest_items WHERE digest_id = $1 AND state <> 'sent'`, digestID).Scan(&left); err != nil {
		return res, err
	}
	if left == 0 {
		if _, err := db.Exec(ctx, `UPDATE digests SET finished_at = $2 WHERE id = $1`, digestID, p.now()); err != nil {
			return res, err
		}
		res.Finished = true
	}
	return res, nil
}

// selectDigest chooses candidates and writes digest_items (state 'selected') before any summarizing.
func (p *pipeline) selectDigest(ctx context.Context, digestID int64) (int, error) {
	s := p.cfg.Settings
	now := p.now()
	since := now.Add(-time.Duration(s.ClusterWindowHrs) * time.Hour)
	rows, err := db.Query(ctx, `
		SELECT c.id, COALESCE(c.section,''), c.topics, c.hot_score, c.n_sources, c.last_sent_n_sources IS NOT NULL
		FROM clusters c
		WHERE c.section IS NOT NULL AND c.n_sources > 0
		  AND ((c.last_sent_n_sources IS NULL AND c.first_seen > $1)
		    OR (c.last_sent_n_sources IS NOT NULL AND c.last_seen > $1 AND c.n_sources >= c.last_sent_n_sources + $2))
		ORDER BY c.hot_score DESC LIMIT 300`, since, s.ResendMinNewSrc)
	if err != nil {
		return 0, err
	}
	var cands []brain.Candidate
	for rows.Next() {
		var c brain.Candidate
		if err := rows.Scan(&c.ClusterID, &c.Section, &c.Topics, &c.Hot, &c.NSources, &c.Resend); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	picked := brain.Select(cands, brain.SelectConfig{Size: s.DigestSize, MaxPerSection: s.MaxPerSection, MinVN: s.MinVN, MaxPerTopic: s.MaxPerTopic})
	for i, c := range picked {
		if _, err := db.Exec(ctx, `INSERT INTO digest_items (digest_id, cluster_id, rank) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, digestID, c.ClusterID, i+1); err != nil {
			return 0, err
		}
	}
	return len(picked), nil
}

type articleRef struct {
	url, title, snippet, source, lang string
}

// clusterArticles returns up to 3 items to summarize: best source, a VN source if any, then next best.
func (p *pipeline) clusterArticles(ctx context.Context, clusterID int64) ([]articleRef, error) {
	rows, err := db.Query(ctx, `SELECT i.url_canonical, i.title, COALESCE(i.snippet,''), i.source_id, COALESCE(s.lang,'')
		FROM items i JOIN sources s ON s.id = i.source_id WHERE i.cluster_id = $1
		ORDER BY s.weight DESC, i.published_at ASC, i.id ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []articleRef
	for rows.Next() {
		var a articleRef
		if err := rows.Scan(&a.url, &a.title, &a.snippet, &a.source, &a.lang); err != nil {
			return nil, err
		}
		all = append(all, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []articleRef
	used := map[string]bool{}
	add := func(a articleRef) {
		if len(out) < 3 && !used[a.source] {
			used[a.source] = true
			out = append(out, a)
		}
	}
	if len(all) > 0 {
		add(all[0])
	}
	for _, a := range all {
		if a.lang == "vi" {
			add(a)
			break
		}
	}
	for _, a := range all {
		add(a)
	}
	return out, nil
}

func (p *pipeline) summarizePending(ctx context.Context, digestID int64, final bool, res *DigestResult) error {
	rows, err := db.Query(ctx, `SELECT cluster_id FROM digest_items WHERE digest_id = $1 AND state = 'selected' ORDER BY rank`, digestID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, cid := range ids {
		arts, err := p.clusterArticles(ctx, cid)
		if err != nil {
			return err
		}
		if len(arts) == 0 {
			continue
		}
		sum, fallback, err := p.summarizeCluster(ctx, arts)
		if err != nil {
			if final {
				sum, fallback = brain.FallbackSummary(arts[0].title, arts[0].snippet), true
			} else {
				rlog.Warn("chưa tóm tắt được, để lượt sau", "cluster", cid, "err", err.Error())
				if errors.Is(err, llm.ErrBudgetExhausted) {
					return nil // hết quota: các tin sau cũng sẽ lỗi
				}
				continue
			}
		}
		sj, _ := json.Marshal(sum)
		if _, err := db.Exec(ctx, `UPDATE digest_items SET state = 'summarized', summary = $3::jsonb, fallback = $4
			WHERE digest_id = $1 AND cluster_id = $2`, digestID, cid, string(sj), fallback); err != nil {
			return err
		}
		res.Summarized++
	}
	return nil
}

func (p *pipeline) summarizeCluster(ctx context.Context, arts []articleRef) (brain.Summary, bool, error) {
	var in []brain.Article
	allLow := true
	for _, a := range arts {
		text := a.snippet
		if r, err := p.article(ctx, a.url); err == nil && !r.LowContext {
			text, allLow = r.Text, false
		}
		in = append(in, brain.Article{Source: p.sourceName(a.source), Title: a.title, Text: text})
	}
	var sum brain.Summary
	if _, err := p.llm.GenerateJSON(ctx, p.cfg.Settings.SummaryModels, brain.SummarizePrompt, brain.BuildSummaryUser(in), brain.SummarySchema(), &sum); err != nil {
		return sum, false, err
	}
	if err := brain.ValidateSummary(&sum); err != nil {
		return sum, false, fmt.Errorf("tóm tắt không hợp lệ: %w", err)
	}
	if allLow {
		sum.LowContext = true
	}
	return sum, false, nil
}

type sendRow struct {
	clusterID int64
	rank      int
	nSources  int
	section   string
	summary   []byte
	fallback  bool
	resend    bool
}

func (p *pipeline) sendPending(ctx context.Context, digestID int64, headerSent *bool, res *DigestResult) error {
	rows, err := db.Query(ctx, `
		SELECT di.cluster_id, di.rank, c.n_sources, COALESCE(c.section,''), COALESCE(di.summary, '{}'::jsonb)::text, di.fallback,
		  EXISTS (SELECT 1 FROM digest_items d2 WHERE d2.cluster_id = di.cluster_id AND d2.digest_id <> di.digest_id AND d2.state = 'sent')
		FROM digest_items di JOIN clusters c ON c.id = di.cluster_id
		WHERE di.digest_id = $1 AND di.state = 'summarized' ORDER BY di.rank`, digestID)
	if err != nil {
		return err
	}
	var todo []sendRow
	for rows.Next() {
		var r sendRow
		var sj string
		if err := rows.Scan(&r.clusterID, &r.rank, &r.nSources, &r.section, &sj, &r.fallback, &r.resend); err != nil {
			rows.Close()
			return err
		}
		r.summary = []byte(sj)
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(todo) == 0 {
		return nil
	}

	first := true
	pace := func() error {
		if first {
			first = false
			return nil
		}
		return p.wait(ctx, p.cfg.Settings.TelegramPacing)
	}

	if !*headerSent {
		if err := pace(); err != nil {
			return err
		}
		if err := p.sendHeader(ctx, digestID); err != nil {
			return err
		}
		*headerSent = true
	}
	for _, r := range todo {
		if err := pace(); err != nil {
			return err
		}
		var sum brain.Summary
		if err := json.Unmarshal(r.summary, &sum); err != nil {
			return err
		}
		names, btn, err := p.clusterLinks(ctx, r.clusterID)
		if err != nil {
			return err
		}
		text := telegram.RenderItem(telegram.Entry{
			Rank: r.rank, Section: r.section, NSources: r.nSources, Summary: sum,
			Fallback: r.fallback, Resend: r.resend, SourceNames: names, URL: btn,
		})
		msgID, err := p.tg.SendMessage(ctx, text, telegram.ItemMarkup(btn))
		if err != nil {
			return fmt.Errorf("gửi tin #%d: %w", r.rank, err)
		}
		if _, err := db.Exec(ctx, `UPDATE digest_items SET state = 'sent', tg_message_id = $3 WHERE digest_id = $1 AND cluster_id = $2`, digestID, r.clusterID, msgID); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, `UPDATE clusters SET last_sent_n_sources = n_sources WHERE id = $1`, r.clusterID); err != nil {
			return err
		}
		res.Sent++
	}
	return nil
}

func (p *pipeline) sendHeader(ctx context.Context, digestID int64) error {
	rows, err := db.Query(ctx, `SELECT COALESCE(c.section,''), c.n_sources FROM digest_items di JOIN clusters c ON c.id = di.cluster_id WHERE di.digest_id = $1`, digestID)
	if err != nil {
		return err
	}
	by := map[string]int{}
	total, hot := 0, 0
	for rows.Next() {
		var sec string
		var n int
		if err := rows.Scan(&sec, &n); err != nil {
			rows.Close()
			return err
		}
		by[sec]++
		total++
		if n >= 3 {
			hot++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := p.tg.SendMessage(ctx, telegram.RenderHeader(vnDate(p.now()), total, by, hot), nil); err != nil {
		return fmt.Errorf("gửi header: %w", err)
	}
	_, err = db.Exec(ctx, `UPDATE digests SET header_sent = true WHERE id = $1`, digestID)
	return err
}

// clusterLinks returns the source names and the URL for the 📖 button.
func (p *pipeline) clusterLinks(ctx context.Context, clusterID int64) ([]string, string, error) {
	rows, err := db.Query(ctx, `SELECT i.source_id, i.url_canonical FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.cluster_id = $1 ORDER BY s.weight DESC, i.published_at ASC, i.id ASC`, clusterID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var names []string
	seen := map[string]bool{}
	direct := ""
	for rows.Next() {
		var sid, u string
		if err := rows.Scan(&sid, &u); err != nil {
			return nil, "", err
		}
		if direct == "" {
			direct = u
		}
		if !seen[sid] {
			seen[sid] = true
			names = append(names, p.sourceName(sid))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(names) > 4 {
		names = names[:4]
	}
	btn := direct
	if p.publicBase != "" {
		btn = fmt.Sprintf("%s/r/%d", strings.TrimRight(p.publicBase, "/"), clusterID)
	}
	return names, btn, nil
}
