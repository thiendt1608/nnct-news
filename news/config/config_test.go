package config

import (
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) < 15 {
		t.Fatalf("sources: %d", len(c.Sources))
	}
	var vb *Source
	for i := range c.Sources {
		if c.Sources[i].ID == "venturebeat-ai" {
			vb = &c.Sources[i]
		}
	}
	if vb == nil || !vb.Disabled {
		t.Fatal("venturebeat-ai phải bị disabled")
	}
	agg := map[string]bool{}
	for _, s := range c.Sources {
		agg[s.ID] = s.Aggregator
	}
	if !agg["hn-top"] || !agg["lobsters"] || agg["techcrunch"] {
		t.Fatalf("aggregator flag sai: %v", agg)
	}
	if c.Settings.Models["gemini-embedding-2"].DailyBudget != 800 {
		t.Fatal("budget embed sai")
	}
	if c.Profile.Topics["ai-models"] != 1.0 {
		t.Fatal("profile sai")
	}
}

func TestParseErrors(t *testing.T) {
	good := "sources:\n  - {id: a, kind: rss, url: u, section: vn}\n"
	prof := "topics: {}\n"
	set := "models: {m: {}}\ntriage_models: [m]\nsummary_models: [m]\nembedding_model: m\nembedding_dim: 768\ndigest_size: 20\ntriage_batch: 40\nembed_batch: 80\ncluster_threshold: 0.8\n"
	if _, err := Parse([]byte(good), []byte(prof), []byte(set)); err != nil {
		t.Fatal(err)
	}
	cases := map[string][3]string{
		"thiếu url":      {"sources:\n  - {id: a, kind: rss, section: vn}\n", prof, set},
		"kind không":     {"sources:\n  - {id: a, kind: x, url: u, section: vn}\n", prof, set},
		"trùng id":       {good + "  - {id: a, kind: rss, url: u, section: vn}\n", prof, set},
		"model không có": {good, prof, strings.Replace(set, "triage_models: [m]", "triage_models: [zz]", 1)},
		"threshold":      {good, prof, strings.Replace(set, "0.8", "1.5", 1)},
	}
	for name, c := range cases {
		if _, err := Parse([]byte(c[0]), []byte(c[1]), []byte(c[2])); err == nil {
			t.Errorf("%s: kỳ vọng lỗi", name)
		}
	}
}
