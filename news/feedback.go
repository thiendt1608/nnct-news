package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"encore.app/news/brain"
	"encore.app/news/telegram"
)

var errClusterNotFound = errors.New("cluster không tồn tại")

// recordFeedback stores one 👍/👎/🔖 for a cluster and applies the learning in the same
// transaction. applied is false when that kind was already recorded (bấm lại không cộng dồn).
func (p *pipeline) recordFeedback(ctx context.Context, clusterID int64, kind string) (applied bool, err error) {
	dt, ds, ok := brain.FeedbackDelta(kind)
	if !ok {
		return false, fmt.Errorf("feedback kind %q không học được", kind)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // lỗi sau Commit là bình thường

	var topics []string
	if err := tx.QueryRow(ctx, `SELECT topics FROM clusters WHERE id = $1`, clusterID).Scan(&topics); err != nil {
		if isNoRows(err) {
			return false, errClusterNotFound
		}
		return false, err
	}
	res, err := tx.Exec(ctx, `INSERT INTO feedback (cluster_id, kind) VALUES ($1, $2) ON CONFLICT DO NOTHING`, clusterID, kind)
	if err != nil {
		return false, err
	}
	if res.RowsAffected() == 0 {
		return false, nil
	}

	if len(topics) > 3 {
		topics = topics[:3] // cùng giới hạn với triage
	}
	if _, err := tx.Exec(ctx, `INSERT INTO profile (key, value) VALUES ('topic_adj', '{}') ON CONFLICT DO NOTHING`); err != nil {
		return false, err
	}
	var raw string
	if err := tx.QueryRow(ctx, `SELECT value::text FROM profile WHERE key = 'topic_adj' FOR UPDATE`).Scan(&raw); err != nil {
		return false, err
	}
	adj := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &adj); err != nil {
		return false, fmt.Errorf("profile.topic_adj hỏng: %w", err)
	}
	b, _ := json.Marshal(brain.AdjustTopics(p.cfg.Profile.Topics, adj, topics, dt))
	if _, err := tx.Exec(ctx, `UPDATE profile SET value = $1::jsonb WHERE key = 'topic_adj'`, string(b)); err != nil {
		return false, err
	}

	rows, err := tx.Query(ctx, `SELECT DISTINCT source_id FROM items WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return false, err
	}
	var srcs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		if !p.isAggregator(id) { // HN/Lobsters chỉ là kênh, không phải nguồn để thưởng/phạt
			srcs = append(srcs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, id := range srcs {
		// kẹp weight + weight_adj trong [MinWeight, MaxWeight]
		if _, err := tx.Exec(ctx, `UPDATE sources SET weight_adj = GREATEST($3::real - weight, LEAST($4::real - weight, weight_adj + $2::real)) WHERE id = $1`,
			id, ds, brain.MinWeight, brain.MaxWeight); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// topicWeights returns the profile topic weights with the learned adjustments applied.
func (p *pipeline) topicWeights(ctx context.Context) (map[string]float64, error) {
	var raw string
	if err := db.QueryRow(ctx, `SELECT value::text FROM profile WHERE key = 'topic_adj'`).Scan(&raw); err != nil {
		if isNoRows(err) {
			return p.cfg.Profile.Topics, nil
		}
		return nil, err
	}
	adj := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &adj); err != nil {
		return nil, fmt.Errorf("profile.topic_adj hỏng: %w", err)
	}
	return brain.EffectiveTopics(p.cfg.Profile.Topics, adj), nil
}

// voted reports which feedback buttons of a cluster were already pressed.
func (p *pipeline) voted(ctx context.Context, clusterID int64) (telegram.Voted, error) {
	rows, err := db.Query(ctx, `SELECT kind FROM feedback WHERE cluster_id = $1 AND kind IN ('up', 'down', 'save')`, clusterID)
	if err != nil {
		return telegram.Voted{}, err
	}
	defer rows.Close()
	var v telegram.Voted
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return v, err
		}
		switch k {
		case "up":
			v.Up = true
		case "down":
			v.Down = true
		case "save":
			v.Save = true
		}
	}
	return v, rows.Err()
}
