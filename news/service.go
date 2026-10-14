package news

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // container có thể thiếu zoneinfo; digest_date theo Asia/Ho_Chi_Minh

	"encore.dev"

	"encore.app/news/config"
	"encore.app/news/extract"
	"encore.app/news/llm"
	"encore.app/news/telegram"
)

var secrets struct {
	TelegramBotToken      string
	TelegramWebhookSecret string // dùng ở phase 2 (webhook)
	TelegramChatID        string
	GeminiAPIKey          string
}

var vnLoc = func() *time.Location {
	l, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return time.FixedZone("ICT", 7*3600)
	}
	return l
}()

// LLM is the subset of llm.Client the pipeline needs (fakeable in tests).
type LLM interface {
	GenerateJSON(ctx context.Context, chain []string, system, user string, schema map[string]any, out any) (string, error)
	Embed(ctx context.Context, model string, texts []string, dim int) ([][]float32, error)
	Remaining(ctx context.Context, model string) (int, error)
}

// Sender is the subset of telegram.Client the pipeline needs.
type Sender interface {
	SendMessage(ctx context.Context, text string, m *telegram.Markup) (int64, error)
}

// Fetcher downloads an article (fakeable in tests).
type Fetcher func(ctx context.Context, url string) (extract.Result, error)

type pipeline struct {
	cfg     *config.Config
	feed    *http.Client
	llm     LLM
	tg      Sender
	article Fetcher
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error
	// publicBase is the public API base URL for /r/:id buttons; empty → link thẳng bài gốc.
	publicBase string

	runMu sync.Mutex
}

func (p *pipeline) sourceByID(id string) (config.Source, bool) {
	for _, s := range p.cfg.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return config.Source{}, false
}

func (p *pipeline) sourceName(id string) string {
	if s, ok := p.sourceByID(id); ok {
		return s.Name
	}
	return id
}

func (p *pipeline) wait(ctx context.Context, d time.Duration) error {
	if p.sleep != nil {
		return p.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var (
	pOnce sync.Once
	pInst *pipeline
	pErr  error
)

func getPipeline() (*pipeline, error) {
	pOnce.Do(func() { pInst, pErr = newPipeline() })
	return pInst, pErr
}

func newPipeline() (*pipeline, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	models := map[string]llm.ModelCfg{}
	for name, m := range cfg.Settings.Models {
		models[name] = llm.ModelCfg{MinInterval: m.MinInterval, TokensPerMin: m.TokensPerMin, DailyBudget: m.DailyBudget}
	}
	p := &pipeline{
		cfg:  cfg,
		feed: &http.Client{Timeout: 15 * time.Second},
		llm: &llm.Client{
			APIKey: func() string { return secrets.GeminiAPIKey },
			Models: models,
			Usage:  dbUsage{},
		},
		tg: &telegram.Client{
			Token:  func() string { return secrets.TelegramBotToken },
			ChatID: func() string { return secrets.TelegramChatID },
		},
		now: time.Now,
	}
	art := extract.NewClient(false)
	p.article = func(ctx context.Context, u string) (extract.Result, error) { return extract.Fetch(ctx, art, u) }
	base := strings.TrimRight(encore.Meta().APIBaseURL.String(), "/")
	if strings.HasPrefix(base, "https://") && !strings.Contains(base, "localhost") {
		p.publicBase = base
	}
	return p, nil
}

// vnDate returns the Vietnam-local calendar date of t.
func vnDate(t time.Time) time.Time {
	l := t.In(vnLoc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}
