package brain

import "sort"

type Candidate struct {
	ClusterID int64
	Section   string // vn | ai | tech | dev
	Topics    []string
	Hot       float64
	NSources  int
	Resend    bool // đã gửi trước đó, n_sources tăng đủ nhiều → nhãn 🔄
}

type SelectConfig struct {
	Size          int
	MaxPerSection int
	MinVN         int
	MaxPerTopic   int
}

func (c Candidate) topic() string {
	if len(c.Topics) == 0 {
		return "other"
	}
	return c.Topics[0]
}

// Select picks up to cfg.Size candidates by hot score under the diversity constraints:
// ≤ MaxPerSection per section, ≤ MaxPerTopic per primary topic, and at least MinVN
// Vietnamese items when enough exist. It never exceeds a cap to reach Size, so it may
// return fewer than Size. Result is ordered by hot descending.
func Select(cands []Candidate, cfg SelectConfig) []Candidate {
	sorted := append([]Candidate(nil), cands...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Hot > sorted[j].Hot })

	picked := map[int64]bool{}
	var out []Candidate
	sec, top := map[string]int{}, map[string]int{}
	try := func(c Candidate) bool {
		if len(out) >= cfg.Size || picked[c.ClusterID] || sec[c.Section] >= cfg.MaxPerSection || top[c.topic()] >= cfg.MaxPerTopic {
			return false
		}
		picked[c.ClusterID] = true
		sec[c.Section]++
		top[c.topic()]++
		out = append(out, c)
		return true
	}
	// 1) giữ chỗ cho tin VN trước
	vn := 0
	for _, c := range sorted {
		if vn >= cfg.MinVN {
			break
		}
		if c.Section == "vn" && try(c) {
			vn++
		}
	}
	// 2) lấp đầy theo hot
	for _, c := range sorted {
		try(c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hot > out[j].Hot })
	return out
}
