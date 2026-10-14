// spike-gemini answers the "not verified" questions in docs/DESIGN.md §9.
// Reads the key from env GEMINI_API_KEY (never hardcode, never commit).
//
//	GEMINI_API_KEY=... go run ./tools/spike-gemini
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const base = "https://generativelanguage.googleapis.com/v1beta/models/"

var key = os.Getenv("GEMINI_API_KEY")

func post(model, method string, body any) (int, []byte) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+model+":"+method, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func trunc(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "…"
	}
	return string(b)
}

func main() {
	if key == "" {
		fmt.Println("set GEMINI_API_KEY")
		os.Exit(2)
	}
	schema := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"context":    map[string]any{"type": "STRING"},
			"key_points": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
		},
		"required": []string{"context", "key_points"},
	}
	// Q1: structured output per model. Model IDs may differ — adjust from AI Studio.
	for _, m := range []string{"gemini-3.1-flash-lite", "gemini-3.5-flash-lite", "gemma-4-31b-it"} {
		code, out := post(m, "generateContent", map[string]any{
			"contents": []any{map[string]any{"parts": []any{map[string]any{"text": "Tóm tắt bằng tiếng Việt: Go 1.26 ra mắt với cải tiến GC."}}}},
			"generationConfig": map[string]any{
				"responseMimeType": "application/json",
				"responseSchema":   schema,
			},
		})
		fmt.Printf("[structured] %s -> %d %s\n\n", m, code, trunc(out))
		time.Sleep(9 * time.Second)
	}
	// Q2: embedding 768 dims; Q3: batch of 3 -> count as 1 or N requests (check usage in AI Studio after).
	var reqs []any
	for _, t := range []string{"một", "hai", "ba"} {
		reqs = append(reqs, map[string]any{
			"model":                "models/gemini-embedding-2",
			"content":              map[string]any{"parts": []any{map[string]any{"text": t}}},
			"outputDimensionality": 768,
		})
	}
	code, out := post("gemini-embedding-2", "batchEmbedContents", map[string]any{"requests": reqs})
	var parsed struct {
		Embeddings []struct{ Values []float64 } `json:"embeddings"`
	}
	_ = json.Unmarshal(out, &parsed)
	dims := 0
	if len(parsed.Embeddings) > 0 {
		dims = len(parsed.Embeddings[0].Values)
	}
	fmt.Printf("[embed] status=%d n=%d dims=%d (%s)\n", code, len(parsed.Embeddings), dims, trunc(out[:min(len(out), 120)]))
}
