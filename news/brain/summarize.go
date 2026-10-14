package brain

import (
	"fmt"
	"strings"
)

type Glossary struct {
	Term    string `json:"term"`
	Explain string `json:"explain"`
}

type Summary struct {
	HeadlineVI   string     `json:"headline_vi"`
	Context      string     `json:"context"`
	KeyPoints    []string   `json:"key_points"`
	WhyItMatters string     `json:"why_it_matters"`
	Glossary     []Glossary `json:"glossary"`
	Caveats      string     `json:"caveats"`
	LowContext   bool       `json:"low_context"`
}

func SummarySchema() map[string]any {
	return map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"headline_vi":    str(),
			"context":        str(),
			"key_points":     map[string]any{"type": "ARRAY", "items": str()},
			"why_it_matters": str(),
			"glossary": map[string]any{"type": "ARRAY", "items": map[string]any{
				"type":       "OBJECT",
				"properties": map[string]any{"term": str(), "explain": str()},
				"required":   []string{"term", "explain"},
			}},
			"caveats":     str(),
			"low_context": map[string]any{"type": "BOOLEAN"},
		},
		"required": []string{"headline_vi", "context", "key_points", "why_it_matters", "low_context"},
	}
}

// Article is one source text fed to the summarizer.
type Article struct {
	Source string
	Title  string
	Text   string
}

// BuildSummaryUser wraps the articles as data for the prompt.
func BuildSummaryUser(arts []Article) string {
	var b strings.Builder
	for i, a := range arts {
		fmt.Fprintf(&b, "=== Bài %d — %s ===\nTiêu đề: %s\n%s\n\n", i+1, a.Source, a.Title, a.Text)
	}
	return b.String()
}

// ValidateSummary normalizes a model summary; error when required fields are missing.
func ValidateSummary(s *Summary) error {
	s.HeadlineVI = strings.TrimSpace(s.HeadlineVI)
	s.Context = strings.TrimSpace(s.Context)
	s.WhyItMatters = strings.TrimSpace(s.WhyItMatters)
	var kp []string
	for _, k := range s.KeyPoints {
		if k = strings.TrimSpace(k); k != "" {
			kp = append(kp, k)
		}
	}
	if len(kp) > 5 {
		kp = kp[:5]
	}
	s.KeyPoints = kp
	if len(s.Glossary) > 2 {
		s.Glossary = s.Glossary[:2]
	}
	switch {
	case s.HeadlineVI == "":
		return fmt.Errorf("thiếu headline_vi")
	case s.Context == "":
		return fmt.Errorf("thiếu context")
	case len(s.KeyPoints) < 3 && !s.LowContext:
		return fmt.Errorf("key_points %d < 3", len(s.KeyPoints))
	case len(s.KeyPoints) == 0:
		return fmt.Errorf("thiếu key_points")
	case s.WhyItMatters == "":
		return fmt.Errorf("thiếu why_it_matters")
	}
	return nil
}

// FallbackSummary builds a minimal summary from title + snippet (⚠️ chưa tóm tắt được).
func FallbackSummary(title, snippet string) Summary {
	return Summary{HeadlineVI: title, Context: snippet, LowContext: true}
}
