package brain

// Giới hạn trọng số sau khi học (docs/DESIGN.md §6.3).
const (
	MinWeight = 0.2
	MaxWeight = 2.0

	// trọng số topic khi profile.yaml không có
	unknownTopicWeight = 0.5
)

// FeedbackDelta returns the topic and source weight change for a feedback kind.
// 👍 và 🔖 cộng, 👎 trừ gấp đôi; kind khác (click, src) không học.
func FeedbackDelta(kind string) (topic, source float64, ok bool) {
	switch kind {
	case "up", "save":
		return 0.05, 0.02, true
	case "down":
		return -0.10, -0.04, true
	}
	return 0, 0, false
}

func clamp(v float64) float64 { return max(MinWeight, min(MaxWeight, v)) }

// AdjustTopics returns adj with delta added to every topic, keeping base+adj inside
// [MinWeight, MaxWeight]. The input map is not modified.
func AdjustTopics(base, adj map[string]float64, topics []string, delta float64) map[string]float64 {
	out := make(map[string]float64, len(adj)+len(topics))
	for k, v := range adj {
		out[k] = v
	}
	for _, t := range topics {
		b, ok := base[t]
		if !ok {
			b = unknownTopicWeight
		}
		out[t] = clamp(b+out[t]+delta) - b
	}
	return out
}

// EffectiveTopics merges the profile weights with the learned adjustments.
func EffectiveTopics(base, adj map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(base)+len(adj))
	for k, v := range base {
		out[k] = v
	}
	for k, a := range adj {
		b, ok := base[k]
		if !ok {
			b = unknownTopicWeight
		}
		out[k] = clamp(b + a)
	}
	return out
}
