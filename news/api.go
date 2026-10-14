package news

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"encore.dev"
	"encore.dev/beta/errs"
	"encore.dev/cron"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

// Ingest fetches feeds and advances the triage/embed/cluster queues.
//
//encore:api private
func Ingest(ctx context.Context) error {
	p, err := getPipeline()
	if err != nil {
		return err
	}
	_, err = p.runIngest(ctx)
	return err
}

// Digest builds and sends today's digest (idempotent, resumable). It runs twice a day
// (06:xx and 07:xx ICT); the second run is "final" and falls back to title+snippet.
//
//encore:api private
func Digest(ctx context.Context) error {
	p, err := getPipeline()
	if err != nil {
		return err
	}
	final := p.now().In(vnLoc).Hour() >= 7
	res, err := p.runDigest(ctx, final)
	rlog.Info("digest", "date", res.Date, "already_done", res.AlreadyDone, "selected", res.Selected,
		"summarized", res.Summarized, "sent", res.Sent, "finished", res.Finished, "final", final)
	return err
}

var _ = cron.NewJob("ingest", cron.JobConfig{
	Title:    "Ingest tin (fetch → triage → embed → cluster)",
	Every:    2 * cron.Hour,
	Endpoint: Ingest,
})

// 23:00 và 00:00 UTC = 06:00 và 07:00 ICT; Encore free tier random phút.
var _ = cron.NewJob("digest", cron.JobConfig{
	Title:    "Digest buổi sáng",
	Schedule: "0 23,0 * * *",
	Endpoint: Digest,
})

// Redirect sends the reader to the best original article of a cluster and records a click.
// The target comes only from the DB, never from the request.
//
//encore:api public raw path=/r/:id
func Redirect(w http.ResponseWriter, req *http.Request) {
	redirectCluster(w, req, encore.CurrentRequest().PathParams.Get("id"))
}

func redirectCluster(w http.ResponseWriter, req *http.Request, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, req)
		return
	}
	ctx := req.Context()
	var target string
	err = db.QueryRow(ctx, `SELECT i.url_canonical FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.cluster_id = $1 ORDER BY s.weight DESC, i.published_at ASC, i.id ASC LIMIT 1`, id).Scan(&target)
	if errors.Is(err, sqldb.ErrNoRows) {
		http.NotFound(w, req)
		return
	}
	if err != nil {
		errs.HTTPError(w, err)
		return
	}
	if _, err := db.Exec(ctx, `INSERT INTO feedback (cluster_id, kind) VALUES ($1, 'click')`, id); err != nil {
		rlog.Warn("không ghi được click", "err", err.Error())
	}
	http.Redirect(w, req, target, http.StatusFound)
}
