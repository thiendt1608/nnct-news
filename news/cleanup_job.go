package news

import (
	"context"
	"fmt"
	"time"
)

// CleanupStats is what one cleanup run removed.
type CleanupStats struct {
	ItemsDeleted     int64
	EmbeddingsShrunk int64
	CentroidsShrunk  int64
	ClustersDeleted  int64
	UsageDeleted     int64
	DBSizeBytes      int64
}

// kept = cluster đã gửi trong digest hoặc có feedback: giữ bài để /r/:id, 📚 và /saved còn dùng được.
const keptClusters = `(SELECT cluster_id FROM digest_items UNION SELECT cluster_id FROM feedback)`

// runCleanup deletes data older than the retention window, except what a sent or rated
// cluster still needs (title, url, source). Embeddings and centroids are only used
// within the cluster window, so they are dropped from kept rows to save space.
func (p *pipeline) runCleanup(ctx context.Context) (CleanupStats, error) {
	var st CleanupStats
	cut := p.now().Add(-time.Duration(p.cfg.Settings.RetentionDays) * 24 * time.Hour)

	res, err := db.Exec(ctx, `DELETE FROM items WHERE fetched_at < $1
		AND (cluster_id IS NULL OR cluster_id NOT IN `+keptClusters+`)`, cut)
	if err != nil {
		return st, fmt.Errorf("xoá items: %w", err)
	}
	st.ItemsDeleted = res.RowsAffected()

	if res, err = db.Exec(ctx, `UPDATE items SET embedding = NULL WHERE fetched_at < $1 AND embedding IS NOT NULL`, cut); err != nil {
		return st, fmt.Errorf("bỏ embedding: %w", err)
	}
	st.EmbeddingsShrunk = res.RowsAffected()

	if res, err = db.Exec(ctx, `UPDATE clusters SET centroid = NULL WHERE last_seen < $1 AND centroid IS NOT NULL`, cut); err != nil {
		return st, fmt.Errorf("bỏ centroid: %w", err)
	}
	st.CentroidsShrunk = res.RowsAffected()

	if res, err = db.Exec(ctx, `DELETE FROM clusters c WHERE c.last_seen < $1
		AND NOT EXISTS (SELECT 1 FROM items i WHERE i.cluster_id = c.id)
		AND c.id NOT IN `+keptClusters, cut); err != nil {
		return st, fmt.Errorf("xoá clusters: %w", err)
	}
	st.ClustersDeleted = res.RowsAffected()

	if res, err = db.Exec(ctx, `DELETE FROM llm_usage WHERE day < ($1::timestamptz)::date`, cut); err != nil {
		return st, fmt.Errorf("xoá llm_usage: %w", err)
	}
	st.UsageDeleted = res.RowsAffected()

	if err := db.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&st.DBSizeBytes); err != nil {
		return st, fmt.Errorf("đo dung lượng DB: %w", err)
	}
	return st, nil
}
