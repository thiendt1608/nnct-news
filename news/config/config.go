// Package config loads the embedded YAML configuration (sources, profile, settings).
package config

import (
	_ "embed"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed sources.yaml
var sourcesYAML []byte

//go:embed profile.yaml
var profileYAML []byte

//go:embed settings.yaml
var settingsYAML []byte

type Source struct {
	ID        string  `yaml:"id"`
	Name      string  `yaml:"name"`
	Kind      string  `yaml:"kind"` // rss | hn
	URL       string  `yaml:"url"`
	Lang      string  `yaml:"lang"`
	Section   string  `yaml:"section"` // vn | ai | tech | dev
	Weight    float64 `yaml:"weight"`
	MinPoints int     `yaml:"min_points"`
	Disabled  bool    `yaml:"-"`
	Enabled   *bool   `yaml:"enabled"`
}

type Profile struct {
	Topics          map[string]float64 `yaml:"topics"`
	MutedKeywords   []string           `yaml:"muted_keywords"`
	BoostedKeywords []string           `yaml:"boosted_keywords"`
}

type Model struct {
	MinInterval  time.Duration `yaml:"min_interval"`
	TokensPerMin int           `yaml:"tokens_per_min"`
	DailyBudget  int           `yaml:"daily_budget"`
}

type Settings struct {
	Models         map[string]Model `yaml:"models"`
	TriageModels   []string         `yaml:"triage_models"`
	SummaryModels  []string         `yaml:"summary_models"`
	EmbeddingModel string           `yaml:"embedding_model"`
	EmbeddingDim   int              `yaml:"embedding_dim"`

	TriageBatch      int `yaml:"triage_batch"`
	TriageMaxBatches int `yaml:"triage_max_batches_per_run"`
	EmbedBatch       int `yaml:"embed_batch"`
	EmbedMaxBatches  int `yaml:"embed_max_batches_per_run"`
	MinRelevance     int `yaml:"min_relevance"`
	ClusterWindowHrs int `yaml:"cluster_window_hours"`
	MaxItemAgeHrs    int `yaml:"max_item_age_hours"`
	DigestSize       int `yaml:"digest_size"`
	MaxPerSection    int `yaml:"max_per_section"`
	MinVN            int `yaml:"min_vn"`
	MaxPerTopic      int `yaml:"max_per_topic"`
	ResendMinNewSrc  int `yaml:"resend_min_new_sources"`

	ClusterThreshold float64       `yaml:"cluster_threshold"`
	TelegramPacing   time.Duration `yaml:"telegram_pacing"`
}

type Config struct {
	Sources  []Source
	Profile  Profile
	Settings Settings
}

// Load parses the embedded config.
func Load() (*Config, error) {
	return Parse(sourcesYAML, profileYAML, settingsYAML)
}

func Parse(sources, profile, settings []byte) (*Config, error) {
	var c Config
	var sf struct {
		Sources []Source `yaml:"sources"`
	}
	if err := yaml.Unmarshal(sources, &sf); err != nil {
		return nil, fmt.Errorf("sources.yaml: %w", err)
	}
	c.Sources = sf.Sources
	if err := yaml.Unmarshal(profile, &c.Profile); err != nil {
		return nil, fmt.Errorf("profile.yaml: %w", err)
	}
	if err := yaml.Unmarshal(settings, &c.Settings); err != nil {
		return nil, fmt.Errorf("settings.yaml: %w", err)
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for i := range c.Sources {
		s := &c.Sources[i]
		switch {
		case s.ID == "":
			return fmt.Errorf("sources[%d]: thiếu id", i)
		case seen[s.ID]:
			return fmt.Errorf("sources: trùng id %q", s.ID)
		case s.Kind != "rss" && s.Kind != "hn":
			return fmt.Errorf("source %q: kind %q không hợp lệ (rss|hn)", s.ID, s.Kind)
		case s.URL == "":
			return fmt.Errorf("source %q: thiếu url", s.ID)
		case s.Section == "":
			return fmt.Errorf("source %q: thiếu section", s.ID)
		}
		seen[s.ID] = true
		if s.Name == "" {
			s.Name = s.ID
		}
		if s.Weight == 0 {
			s.Weight = 1.0
		}
		s.Disabled = s.Enabled != nil && !*s.Enabled
	}
	st := &c.Settings
	for _, chain := range [][]string{st.TriageModels, st.SummaryModels} {
		if len(chain) == 0 {
			return fmt.Errorf("settings: thiếu triage_models/summary_models")
		}
		for _, m := range chain {
			if _, ok := st.Models[m]; !ok {
				return fmt.Errorf("settings: model %q không có trong models", m)
			}
		}
	}
	if _, ok := st.Models[st.EmbeddingModel]; !ok {
		return fmt.Errorf("settings: embedding_model %q không có trong models", st.EmbeddingModel)
	}
	if st.EmbeddingDim <= 0 || st.DigestSize <= 0 || st.TriageBatch <= 0 || st.EmbedBatch <= 0 {
		return fmt.Errorf("settings: embedding_dim/digest_size/triage_batch/embed_batch phải > 0")
	}
	if st.ClusterThreshold <= 0 || st.ClusterThreshold >= 1 {
		return fmt.Errorf("settings: cluster_threshold phải trong (0,1)")
	}
	return nil
}
