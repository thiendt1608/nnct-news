// check-sources fetches each feed in news/config/sources.yaml and prints item count / errors.
// Usage: go run ./tools/check-sources
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type source struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
	URL  string `yaml:"url"`
}

type result struct {
	id, status string
	items      int
	err        error
}

func main() {
	raw, err := os.ReadFile("news/config/sources.yaml")
	if err != nil {
		panic(err)
	}
	var cfg struct {
		Sources []source `yaml:"sources"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		panic(err)
	}
	res := make([]result, len(cfg.Sources))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, s := range cfg.Sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res[i] = check(s)
		}()
	}
	wg.Wait()
	bad := 0
	for _, r := range res {
		state := "OK  "
		if r.err != nil || r.items == 0 {
			state, bad = "FAIL", bad+1
		}
		fmt.Printf("%s %-20s http=%-4s items=%-3d %v\n", state, r.id, r.status, r.items, r.err)
	}
	if bad > 0 {
		os.Exit(1)
	}
}

func check(s source) result {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	req.Header.Set("User-Agent", "nnct-news/0.1 (personal feed reader)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return result{id: s.ID, err: err}
	}
	defer resp.Body.Close()
	r := result{id: s.ID, status: fmt.Sprint(resp.StatusCode)}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil || resp.StatusCode != 200 {
		r.err = fmt.Errorf("status/read: %v", err)
		return r
	}
	if s.Kind == "hn" {
		var ids []int
		r.err = json.Unmarshal(body, &ids)
		r.items = len(ids)
		return r
	}
	// count <item> (RSS) or <entry> (Atom) tags
	n := strings.Count(string(body), "<item") + strings.Count(string(body), "<entry")
	if err := xml.Unmarshal(body, new(struct{ XMLName xml.Name })); err != nil {
		r.err = fmt.Errorf("not xml: %v", err)
	}
	r.items = n
	return r
}
