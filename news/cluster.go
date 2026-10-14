package news

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"encore.app/news/brain"
)

// cluster assigns embedded items to the nearest recent cluster (cosine ≥ threshold) or
// opens a new one, then refreshes stats + hot score of all live clusters.
func (p *pipeline) cluster(ctx context.Context, st *IngestStats) error {
	s := p.cfg.Settings
	now := p.now()
	since := now.Add(-time.Duration(s.ClusterWindowHrs) * time.Hour)

	rows, err := db.Query(ctx, `SELECT id, embedding::text, COALESCE(published_at, $1) FROM items
		WHERE state = 'embedded' ORDER BY published_at ASC NULLS LAST, id ASC`, now)
	if err != nil {
		return err
	}
	type pending struct {
		id  int64
		vec string
		pub time.Time
	}
	var todo []pending
	for rows.Next() {
		var x pending
		if err := rows.Scan(&x.id, &x.vec, &x.pub); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range todo {
		var cid int64
		var sim float64
		err := db.QueryRow(ctx, `SELECT id, 1 - (centroid <=> $1::halfvec) FROM clusters
			WHERE last_seen > $2 AND centroid IS NOT NULL
			ORDER BY centroid <=> $1::halfvec LIMIT 1`, it.vec, since).Scan(&cid, &sim)
		found := err == nil
		if err != nil && !isNoRows(err) {
			return err
		}
		if found && sim >= s.ClusterThreshold {
			if _, err := db.Exec(ctx, `UPDATE items SET cluster_id = $2, state = 'clustered' WHERE id = $1`, it.id, cid); err != nil {
				return err
			}
			if _, err := db.Exec(ctx, `UPDATE clusters SET last_seen = $2,
				centroid = (SELECT avg(embedding) FROM items WHERE cluster_id = $1) WHERE id = $1`, cid, now); err != nil {
				return err
			}
		} else {
			if err := db.QueryRow(ctx, `INSERT INTO clusters (first_seen, last_seen, centroid) VALUES ($2, $3, $1::halfvec) RETURNING id`,
				it.vec, it.pub, now).Scan(&cid); err != nil {
				return err
			}
			if _, err := db.Exec(ctx, `UPDATE items SET cluster_id = $2, state = 'clustered' WHERE id = $1`, it.id, cid); err != nil {
				return err
			}
		}
		st.Clustered++
	}
	return p.refreshClusters(ctx, since)
}

type memberRow struct {
	sourceID  string
	title     string
	published time.Time
	points    int
	triage    []byte
	section   string
	lang      string
	weight    float64
}

// refreshClusters recomputes n_sources, engagement, topics, section and hot score for every
// cluster seen since `since` (so age decay also applies to quiet clusters).
func (p *pipeline) refreshClusters(ctx context.Context, since time.Time) error {
	crows, err := db.Query(ctx, `SELECT id FROM clusters WHERE last_seen > $1`, since)
	if err != nil {
		return err
	}
	var ids []int64
	for crows.Next() {
		var id int64
		if err := crows.Scan(&id); err != nil {
			crows.Close()
			return err
		}
		ids = append(ids, id)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := p.refreshCluster(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *pipeline) refreshCluster(ctx context.Context, id int64) error {
	rows, err := db.Query(ctx, `SELECT i.source_id, i.title, COALESCE(i.published_at, i.fetched_at),
			COALESCE((i.engagement->>'points')::int, 0), COALESCE(i.triage, '{}'::jsonb)::text,
			s.section, s.lang, s.weight
		FROM items i JOIN sources s ON s.id = i.source_id WHERE i.cluster_id = $1`, id)
	if err != nil {
		return err
	}
	var ms []memberRow
	for rows.Next() {
		var m memberRow
		var tj string
		if err := rows.Scan(&m.sourceID, &m.title, &m.published, &m.points, &tj, &m.section, &m.lang, &m.weight); err != nil {
			rows.Close()
			return err
		}
		m.triage = []byte(tj)
		ms = append(ms, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ms) == 0 {
		return err
	}

	srcSeen := map[string]float64{}
	maxPts, maxRel := 0, 0
	topicCount := map[string]int{}
	var best memberRow
	first := ms[0].published
	var titles []string
	for _, m := range ms {
		srcSeen[m.sourceID] = m.weight
		if m.points > maxPts {
			maxPts = m.points
		}
		if m.weight > best.weight || best.sourceID == "" {
			best = m
		}
		if m.published.Before(first) {
			first = m.published
		}
		titles = append(titles, m.title)
		var tr brain.TriageResult
		if json.Unmarshal(m.triage, &tr) == nil {
			if tr.Relevance > maxRel {
				maxRel = tr.Relevance
			}
			for i, t := range tr.Topics {
				topicCount[t] += 3 - min(i, 2) // chủ đề chính nặng hơn
			}
		}
	}
	var topics []string
	for t := range topicCount {
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool {
		if topicCount[topics[i]] != topicCount[topics[j]] {
			return topicCount[topics[i]] > topicCount[topics[j]]
		}
		return topics[i] < topics[j]
	})
	sumW := 0.0
	for _, w := range srcSeen {
		sumW += w
	}
	in := brain.ScoreInput{
		NSources:    len(srcSeen),
		Engagement:  maxPts,
		AvgWeight:   sumW / float64(len(srcSeen)),
		Relevance:   maxRel,
		TopicWeight: brain.BestTopicWeight(topics, p.cfg.Profile.Topics),
		BoostedHit:  brain.HasBoosted(strings.Join(titles, " "), p.cfg.Profile.BoostedKeywords),
		AgeHours:    p.now().Sub(first).Hours(),
	}
	reason := brain.Score(in)
	rj, _ := json.Marshal(reason)
	_, err = db.Exec(ctx, `UPDATE clusters SET n_sources = $2, max_engagement = $3, section = $4, topics = $5,
		hot_score = $6, hot_reason = $7::jsonb, first_seen = $8 WHERE id = $1`,
		id, len(srcSeen), maxPts, best.section, topics, reason.Total, string(rj), first)
	if err != nil {
		return fmt.Errorf("cluster %d: %w", id, err)
	}
	return nil
}
