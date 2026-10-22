package brain

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestFeedbackDelta(t *testing.T) {
	for kind, want := range map[string][2]float64{"up": {0.05, 0.02}, "save": {0.05, 0.02}, "down": {-0.10, -0.04}} {
		tw, sw, ok := FeedbackDelta(kind)
		if !ok || !near(tw, want[0]) || !near(sw, want[1]) {
			t.Errorf("%s: %v %v %v", kind, tw, sw, ok)
		}
	}
	if _, _, ok := FeedbackDelta("click"); ok {
		t.Error("click không học ở đây")
	}
}

func TestAdjustTopicsClampAndPure(t *testing.T) {
	base := map[string]float64{"ai-models": 1.0, "security": 0.6}
	adj := map[string]float64{"security": 0.1}
	out := AdjustTopics(base, adj, []string{"ai-models", "security", "new-topic"}, -0.1)
	if !near(out["ai-models"], -0.1) || !near(out["security"], 0.0) || !near(out["new-topic"], -0.1) {
		t.Fatalf("%v", out)
	}
	if !near(adj["security"], 0.1) || len(adj) != 1 {
		t.Fatalf("không được sửa map đầu vào: %v", adj)
	}
	// kẹp sàn: security base 0.6, trừ mãi chỉ xuống 0.2
	cur := map[string]float64{}
	for i := 0; i < 20; i++ {
		cur = AdjustTopics(base, cur, []string{"security"}, -0.1)
	}
	if got := EffectiveTopics(base, cur)["security"]; !near(got, MinWeight) {
		t.Fatalf("sàn: %v", got)
	}
	// kẹp trần
	cur = map[string]float64{}
	for i := 0; i < 100; i++ {
		cur = AdjustTopics(base, cur, []string{"ai-models"}, 0.05)
	}
	if got := EffectiveTopics(base, cur)["ai-models"]; !near(got, MaxWeight) {
		t.Fatalf("trần: %v", got)
	}
	// bấm ngược sau khi chạm sàn phải nhích lên ngay, không bị "nợ"
	cur = AdjustTopics(base, cur, []string{"ai-models"}, -0.1)
	if got := EffectiveTopics(base, cur)["ai-models"]; !near(got, 1.9) {
		t.Fatalf("sau trần: %v", got)
	}
}

func TestEffectiveTopicsUnknownBase(t *testing.T) {
	got := EffectiveTopics(map[string]float64{"a": 1}, map[string]float64{"zzz": 0.1})
	if !near(got["a"], 1) || !near(got["zzz"], 0.6) {
		t.Fatalf("%v", got)
	}
}
