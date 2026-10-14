// Package brain holds the pure decision logic: triage, scoring, selection, summaries.
package brain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed prompts/triage.txt
var TriagePrompt string

//go:embed prompts/summarize.txt
var SummarizePrompt string

type TriageInput struct {
	ID      int64
	Title   string
	Snippet string
	Source  string
}

type TriageResult struct {
	ID        int64    `json:"id"`
	Topics    []string `json:"topics"`
	Relevance int      `json:"relevance"`
	Kind      string   `json:"kind"`
	TitleVI   string   `json:"title_vi"`
}

type triageResponse struct {
	Items []TriageResult `json:"items"`
}

func str() map[string]any { return map[string]any{"type": "STRING"} }

// TriageSchema is the Gemini responseSchema for a triage batch.
func TriageSchema() map[string]any {
	return map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{"items": map[string]any{
			"type": "ARRAY",
			"items": map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"id":        map[string]any{"type": "INTEGER"},
					"topics":    map[string]any{"type": "ARRAY", "items": str()},
					"relevance": map[string]any{"type": "INTEGER"},
					"kind":      map[string]any{"type": "STRING", "enum": []string{"news", "opinion", "tutorial", "pr", "deal", "clickbait"}},
					"title_vi":  str(),
				},
				"required": []string{"id", "topics", "relevance", "kind", "title_vi"},
			},
		}},
		"required": []string{"items"},
	}
}

// BuildTriageUser renders the batch as the user message.
func BuildTriageUser(in []TriageInput) string {
	var b strings.Builder
	for _, it := range in {
		fmt.Fprintf(&b, "id=%d | nguồn=%s | tiêu đề: %s | trích: %s\n", it.ID, it.Source, it.Title, it.Snippet)
	}
	return b.String()
}

// TriageResponse is the decoding target for the model output.
type TriageResponse = triageResponse

// ValidateTriage keeps only results whose id is in the batch (first wins) and clamps relevance.
func ValidateTriage(resp triageResponse, in []TriageInput) map[int64]TriageResult {
	want := map[int64]bool{}
	for _, it := range in {
		want[it.ID] = true
	}
	out := map[int64]TriageResult{}
	for _, r := range resp.Items {
		if !want[r.ID] {
			continue
		}
		if _, dup := out[r.ID]; dup {
			continue
		}
		if r.Relevance < 0 {
			r.Relevance = 0
		}
		if r.Relevance > 10 {
			r.Relevance = 10
		}
		out[r.ID] = r
	}
	return out
}

// TriageVerdict decides whether an item survives triage.
func TriageVerdict(r TriageResult, title string, minRelevance int, muted []string) (keep bool, reason string) {
	switch r.Kind {
	case "pr", "deal", "clickbait":
		return false, "kind:" + r.Kind
	}
	if r.Relevance < minRelevance {
		return false, "low-relevance"
	}
	lt := strings.ToLower(title)
	for _, m := range muted {
		if m != "" && strings.Contains(lt, strings.ToLower(m)) {
			return false, "muted:" + m
		}
	}
	return true, ""
}

// TriageJSON is what gets stored in items.triage.
func TriageJSON(r TriageResult) []byte {
	b, _ := json.Marshal(r)
	return b
}
