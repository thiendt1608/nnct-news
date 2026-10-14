package brain

import (
	"fmt"
	"math"
	"testing"
)

func TestTriage(t *testing.T) {
	in := []TriageInput{{ID: 1}, {ID: 2}, {ID: 3}}
	resp := TriageResponse{Items: []TriageResult{
		{ID: 1, Relevance: 15, Kind: "news"}, {ID: 1, Relevance: 2, Kind: "news"}, // dup
		{ID: 2, Relevance: -3, Kind: "news"}, {ID: 99, Relevance: 9},
	}}
	got := ValidateTriage(resp, in)
	if len(got) != 2 || got[1].Relevance != 10 || got[2].Relevance != 0 {
		t.Fatalf("%+v", got)
	}
	if _, ok := got[3]; ok {
		t.Fatal("id 3 không có trong output")
	}
	cases := []struct {
		r     TriageResult
		title string
		keep  bool
	}{
		{TriageResult{Kind: "news", Relevance: 7}, "x", true},
		{TriageResult{Kind: "news", Relevance: 3}, "x", false},
		{TriageResult{Kind: "pr", Relevance: 9}, "x", false},
		{TriageResult{Kind: "deal", Relevance: 9}, "x", false},
		{TriageResult{Kind: "clickbait", Relevance: 9}, "x", false},
		{TriageResult{Kind: "news", Relevance: 9}, "Crypto Airdrop mới", false},
		{TriageResult{Kind: "news", Relevance: 4}, "x", true},
	}
	for i, c := range cases {
		if keep, _ := TriageVerdict(c.r, c.title, 4, []string{"crypto airdrop"}); keep != c.keep {
			t.Errorf("case %d: keep=%v", i, keep)
		}
	}
}

func TestScore(t *testing.T) {
	base := ScoreInput{NSources: 1, AvgWeight: 1, Relevance: 10, TopicWeight: 1}
	b := Score(base)
	want := 1.5*math.Log(2) + 0.8 + 1.2
	if math.Abs(b.Total-want) > 1e-9 {
		t.Fatalf("%v want %v", b.Total, want)
	}
	more := base
	more.NSources = 7
	if Score(more).Total <= b.Total {
		t.Fatal("nhiều nguồn phải nóng hơn")
	}
	old := base
	old.AgeHours = 48
	if d := b.Total - Score(old).Total; math.Abs(d-1.0) > 1e-9 {
		t.Fatalf("decay 48h = 1.0, got %v", d)
	}
	boost := base
	boost.BoostedHit = true
	if d := Score(boost).Total - b.Total; math.Abs(d-0.3) > 1e-9 {
		t.Fatal(d)
	}
	eng := base
	eng.Engagement = 1000
	if d := Score(eng).Total - b.Total; math.Abs(d-1.0) > 1e-9 {
		t.Fatal(d)
	}
	if EngagementNorm(0) != 0 || EngagementNorm(1e9) != 1 {
		t.Fatal("norm kẹp [0,1]")
	}
	if !HasBoosted("Anthropic ra Claude mới", []string{"claude"}) || HasBoosted("abc", []string{"xyz"}) {
		t.Fatal("boosted")
	}
	if BestTopicWeight([]string{"x", "dev-tools"}, map[string]float64{"dev-tools": 0.8}) != 0.8 || BestTopicWeight(nil, nil) != 0.5 {
		t.Fatal("topic weight")
	}
}

func mk(n int, section, topic string, hot float64) Candidate {
	return Candidate{ClusterID: int64(n), Section: section, Topics: []string{topic}, Hot: hot}
}

// AC7
func TestSelectConstraints(t *testing.T) {
	var cands []Candidate
	id := 0
	// 15 tin AI hot cao với 2 topic, 12 tech, 6 VN hot thấp (7+7+6 = 20 là mức tối đa hợp lệ)
	for i := 0; i < 15; i++ {
		id++
		cands = append(cands, mk(id, "ai", []string{"ai-models", "ai-engineering"}[i%2], 10-float64(i)*0.1))
	}
	for i := 0; i < 12; i++ {
		id++
		cands = append(cands, mk(id, "tech", fmt.Sprintf("t%d", i%4), 8-float64(i)*0.1))
	}
	for i := 0; i < 6; i++ {
		id++
		cands = append(cands, mk(id, "vn", fmt.Sprintf("v%d", i), 1-float64(i)*0.1))
	}
	cfg := SelectConfig{Size: 20, MaxPerSection: 7, MinVN: 3, MaxPerTopic: 4}
	got := Select(cands, cfg)
	if len(got) != 20 {
		t.Fatalf("len=%d", len(got))
	}
	sec, top := map[string]int{}, map[string]int{}
	for i, c := range got {
		sec[c.Section]++
		top[c.Topics[0]]++
		if i > 0 && got[i-1].Hot < c.Hot {
			t.Fatal("phải sắp theo hot giảm dần")
		}
	}
	for s, n := range sec {
		if n > 7 {
			t.Errorf("section %s=%d", s, n)
		}
	}
	for tp, n := range top {
		if n > 4 {
			t.Errorf("topic %s=%d", tp, n)
		}
	}
	if sec["vn"] < 3 {
		t.Errorf("vn=%d", sec["vn"])
	}
	// không đủ ứng viên thoả cap → trả ít hơn, không phá cap
	few := Select(cands[:15], cfg) // chỉ AI: cap section 7 chặn trước cap topic 2×4=8
	if len(few) != 7 {
		t.Fatalf("few=%d", len(few))
	}
	// ít VN hơn MinVN: lấy hết số có
	if got := Select([]Candidate{mk(1, "vn", "a", 1), mk(2, "ai", "b", 5)}, cfg); len(got) != 2 {
		t.Fatal("2 ứng viên")
	}
}

func TestSummaryValidate(t *testing.T) {
	ok := Summary{HeadlineVI: "h", Context: "c", KeyPoints: []string{"a", "b", " c ", "", "d", "e", "f"}, WhyItMatters: "w",
		Glossary: []Glossary{{"a", "b"}, {"c", "d"}, {"e", "f"}}}
	if err := ValidateSummary(&ok); err != nil || len(ok.KeyPoints) != 5 || len(ok.Glossary) != 2 {
		t.Fatalf("%v %+v", err, ok)
	}
	bad := []Summary{
		{Context: "c", KeyPoints: []string{"a", "b", "c"}, WhyItMatters: "w"},
		{HeadlineVI: "h", KeyPoints: []string{"a", "b", "c"}, WhyItMatters: "w"},
		{HeadlineVI: "h", Context: "c", KeyPoints: []string{"a"}, WhyItMatters: "w"},
		{HeadlineVI: "h", Context: "c", KeyPoints: []string{"a", "b", "c"}},
	}
	for i := range bad {
		if ValidateSummary(&bad[i]) == nil {
			t.Errorf("bad %d phải lỗi", i)
		}
	}
	low := Summary{HeadlineVI: "h", Context: "c", KeyPoints: []string{"a"}, WhyItMatters: "w", LowContext: true}
	if err := ValidateSummary(&low); err != nil {
		t.Fatalf("low_context cho phép ít ý: %v", err)
	}
}

func TestClusterSection(t *testing.T) {
	cases := []struct {
		topics, srcs []string
		best, want   string
	}{
		{[]string{"ai-models"}, []string{"tech", "tech"}, "tech", "ai"}, // tin OpenAI từ HN/Verge
		{[]string{"ai-engineering"}, []string{"vn"}, "vn", "vn"},        // chỉ báo VN → giữ vn
		{[]string{"ai-models"}, []string{"vn", "tech"}, "tech", "ai"},   // có cả nguồn ngoài → theo chủ đề
		{[]string{"dev-tools"}, []string{"tech"}, "tech", "dev"},
		{[]string{"security"}, []string{"ai"}, "ai", "tech"},
		{[]string{"other"}, []string{"dev"}, "dev", "dev"},
		{nil, []string{"vn", "tech"}, "vn", "tech"},
		{nil, nil, "ai", "ai"},
	}
	for i, c := range cases {
		if got := ClusterSection(c.topics, c.srcs, c.best); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}
