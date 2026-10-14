package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/sync/errgroup"

	"encore.app/news/config"
)

const userAgent = "nnct-news/0.1 (personal feed reader; +https://github.com/8-Sync-Dev)"

// FetchState is the conditional-GET state persisted per source.
type FetchState struct {
	ETag         string
	LastModified string
}

type Result struct {
	Items       []Item
	State       FetchState
	NotModified bool
}

func get(ctx context.Context, c *http.Client, url string, st FetchState) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	// cancel is tied to body close via wrapper below
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if st.ETag != "" {
		req.Header.Set("If-None-Match", st.ETag)
	}
	if st.LastModified != "" {
		req.Header.Set("If-Modified-Since", st.LastModified)
	}
	resp, err := c.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{resp.Body, cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelBody) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

// FetchRSS fetches and parses an RSS/Atom feed. A 304 returns NotModified with no items.
func FetchRSS(ctx context.Context, c *http.Client, src config.Source, st FetchState) (Result, error) {
	resp, err := get(ctx, c, src.URL, st)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return Result{State: st, NotModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("http %d", resp.StatusCode)
	}
	feed, err := gofeed.NewParser().Parse(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return Result{}, fmt.Errorf("parse: %w", err)
	}
	res := Result{State: FetchState{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}}
	for _, e := range feed.Items {
		pub := time.Time{}
		if e.PublishedParsed != nil {
			pub = *e.PublishedParsed
		} else if e.UpdatedParsed != nil {
			pub = *e.UpdatedParsed
		}
		snip := e.Description
		if snip == "" {
			snip = e.Content
		}
		if it, ok := Build(src.ID, src.Lang, e.Link, e.Title, snip, pub); ok {
			res.Items = append(res.Items, it)
		}
	}
	return res, nil
}

type hnItem struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Time        int64  `json:"time"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
}

// FetchHN reads the Firebase top-stories list (src.URL ends with topstories.json),
// then loads the first 60 stories and keeps those with at least src.MinPoints.
func FetchHN(ctx context.Context, c *http.Client, src config.Source) (Result, error) {
	resp, err := get(ctx, c, src.URL, FetchState{})
	if err != nil {
		return Result{}, err
	}
	var ids []int
	err = func() error {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("http %d", resp.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ids)
	}()
	if err != nil {
		return Result{}, err
	}
	if len(ids) > 60 {
		ids = ids[:60]
	}
	base := src.URL[:strings.LastIndex(src.URL, "/")+1]
	var mu sync.Mutex
	var items []Item
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, id := range ids {
		g.Go(func() error {
			r, err := get(gctx, c, fmt.Sprintf("%sitem/%d.json", base, id), FetchState{})
			if err != nil {
				return nil // một story lỗi không làm hỏng cả nguồn
			}
			defer r.Body.Close()
			var h hnItem
			if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&h) != nil {
				return nil
			}
			if h.Type != "story" || h.URL == "" || h.Dead || h.Deleted || h.Score < src.MinPoints {
				return nil
			}
			it, ok := Build(src.ID, src.Lang, h.URL, h.Title, "", time.Unix(h.Time, 0).UTC())
			if !ok {
				return nil
			}
			it.Engagement = map[string]int{"points": h.Score, "comments": h.Descendants}
			mu.Lock()
			items = append(items, it)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return Result{Items: items}, nil
}

// Fetch dispatches by source kind.
func Fetch(ctx context.Context, c *http.Client, src config.Source, st FetchState) (Result, error) {
	switch src.Kind {
	case "rss":
		return FetchRSS(ctx, c, src, st)
	case "hn":
		return FetchHN(ctx, c, src)
	}
	return Result{}, fmt.Errorf("kind %q chưa hỗ trợ", src.Kind)
}
