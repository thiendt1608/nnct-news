package brain

import (
	"math"
	"strings"
)

type ScoreInput struct {
	NSources    int
	Engagement  int     // max HN points / upvotes in the cluster
	AvgWeight   float64 // mean source weight
	Relevance   int     // max triage relevance in the cluster (0-10)
	TopicWeight float64 // best profile weight among the cluster topics
	BoostedHit  bool
	AgeHours    float64
}

type ScoreReason struct {
	Sources    float64 `json:"sources"`
	Engagement float64 `json:"engagement"`
	Weight     float64 `json:"weight"`
	Relevance  float64 `json:"relevance"`
	Boost      float64 `json:"boost"`
	Age        float64 `json:"age"`
	Total      float64 `json:"total"`
}

// EngagementNorm maps raw points to [0,1] on a log scale (1000 points ≈ 1.0).
// DESIGN nói "percentile theo nguồn"; MVP dùng thang log vì chưa có đủ lịch sử để tính percentile.
func EngagementNorm(points int) float64 {
	if points <= 0 {
		return 0
	}
	return math.Min(1, math.Log1p(float64(points))/math.Log1p(1000))
}

// Score implements the hot formula of DESIGN §5.5.
func Score(in ScoreInput) ScoreReason {
	r := ScoreReason{
		Sources:    1.5 * math.Log(1+float64(in.NSources)),
		Engagement: 1.0 * EngagementNorm(in.Engagement),
		Weight:     0.8 * in.AvgWeight,
		Relevance:  1.2 * float64(in.Relevance) / 10 * in.TopicWeight,
		Age:        -0.5 * in.AgeHours / 24,
	}
	if in.BoostedHit {
		r.Boost = 0.3
	}
	r.Total = r.Sources + r.Engagement + r.Weight + r.Relevance + r.Boost + r.Age
	return r
}

// HasBoosted reports whether any boosted keyword appears in the text (case-insensitive).
func HasBoosted(text string, keywords []string) bool {
	lt := strings.ToLower(text)
	for _, k := range keywords {
		if k != "" && strings.Contains(lt, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// BestTopicWeight returns the max profile weight over topics (0.5 when unknown).
func BestTopicWeight(topics []string, profile map[string]float64) float64 {
	best := 0.0
	for _, t := range topics {
		if w, ok := profile[t]; ok && w > best {
			best = w
		}
	}
	if best == 0 {
		return 0.5
	}
	return best
}
