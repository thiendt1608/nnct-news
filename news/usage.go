package news

import (
	"context"
	"errors"

	"encore.dev/storage/sqldb"
)

// dbUsage persists llm request counts per (Pacific day, model) in llm_usage.
type dbUsage struct{}

func (dbUsage) Used(ctx context.Context, day, model string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT requests FROM llm_usage WHERE day = $1::date AND model = $2`, day, model).Scan(&n)
	if errors.Is(err, sqldb.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (dbUsage) Add(ctx context.Context, day, model string, n int) error {
	_, err := db.Exec(ctx, `
		INSERT INTO llm_usage (day, model, requests) VALUES ($1::date, $2, $3)
		ON CONFLICT (day, model) DO UPDATE SET requests = llm_usage.requests + EXCLUDED.requests`,
		day, model, n)
	return err
}
