package telegram

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"encore.app/news/brain"
)

const MaxMessage = 4096

// Escape escapes the three characters Telegram HTML requires.
func Escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var sectionLabel = map[string]string{"ai": "🤖 AI", "tech": "🌍 Tech", "dev": "🛠 Dev", "vn": "🇻🇳 VN"}

func SectionLabel(s string) string {
	if l, ok := sectionLabel[s]; ok {
		return l
	}
	return "📰 " + s
}

// Entry is one digest item ready to render.
type Entry struct {
	Rank        int
	Section     string
	NSources    int
	Summary     brain.Summary
	Fallback    bool
	Resend      bool
	SourceNames []string
	URL         string // nút 📖: /r/:id hoặc link bài gốc
}

// RenderItem renders one message; guaranteed ≤ MaxMessage runes by trimming key points then context.
func RenderItem(e Entry) string {
	points := append([]string(nil), e.Summary.KeyPoints...)
	ctx := e.Summary.Context
	for {
		out := build(e, ctx, points)
		if utf8.RuneCountInString(out) <= MaxMessage {
			return out
		}
		if len(points) > 0 {
			points = points[:len(points)-1]
			continue
		}
		r := []rune(ctx)
		if len(r) <= 200 {
			return string([]rune(out)[:MaxMessage]) // cuối cùng: cắt cứng, không bao giờ xảy ra với tin thật
		}
		ctx = string(r[:len(r)*3/4]) + "…"
	}
}

func build(e Entry, ctx string, points []string) string {
	var b strings.Builder
	head := fmt.Sprintf("#%d · %s", e.Rank, SectionLabel(e.Section))
	if e.NSources >= 3 {
		head = "🔥 " + head
	}
	if e.NSources > 1 {
		head += fmt.Sprintf(" · %d nguồn", e.NSources)
	}
	if e.Resend {
		head += " · 🔄 Cập nhật"
	}
	b.WriteString(Escape(head))
	b.WriteString("\n\n")
	if e.Fallback {
		b.WriteString("⚠️ <i>Chưa tóm tắt được, chỉ có trích đoạn.</i>\n\n")
	}
	fmt.Fprintf(&b, "<b>%s</b>\n\n", Escape(e.Summary.HeadlineVI))
	if ctx != "" {
		b.WriteString(Escape(ctx))
		b.WriteString("\n\n")
	}
	if len(points) > 0 {
		b.WriteString("🔍 Điểm chính\n")
		for _, p := range points {
			b.WriteString("• " + Escape(p) + "\n")
		}
		b.WriteString("\n")
	}
	if w := e.Summary.WhyItMatters; w != "" {
		b.WriteString("💡 Vì sao đáng quan tâm: " + Escape(w) + "\n\n")
	}
	for _, g := range e.Summary.Glossary {
		fmt.Fprintf(&b, "🧩 <i>%s</i>: %s\n", Escape(g.Term), Escape(g.Explain))
	}
	if len(e.Summary.Glossary) > 0 {
		b.WriteString("\n")
	}
	if c := e.Summary.Caveats; c != "" {
		b.WriteString("⚠️ " + Escape(c) + "\n\n")
	}
	if len(e.SourceNames) > 0 {
		b.WriteString("📰 " + Escape(strings.Join(e.SourceNames, " · ")))
	}
	return strings.TrimRight(b.String(), "\n")
}

// Voted is which feedback buttons were already pressed on a cluster.
type Voted struct{ Up, Down, Save bool }

// CallbackData builds the callback_data of a button ("up:12", "src:12", ...), ≤ 64 bytes.
func CallbackData(action string, clusterID int64) string {
	return fmt.Sprintf("%s:%d", action, clusterID)
}

// ItemMarkup is the keyboard under each item: link buttons on top, feedback below.
// Nút đã bấm đổi thành ✅.
func ItemMarkup(url string, clusterID int64, v Voted) *Markup {
	btn := func(done bool, label, doneLabel, action string) Button {
		if done {
			label = "✅ " + doneLabel
		}
		return Button{Text: label, CallbackData: CallbackData(action, clusterID)}
	}
	return &Markup{InlineKeyboard: [][]Button{
		{{Text: "📖 Đọc bài gốc", URL: url}, {Text: "📚 Nguồn khác", CallbackData: CallbackData("src", clusterID)}},
		{btn(v.Up, "👍", "👍", "up"), btn(v.Down, "👎", "👎", "down"), btn(v.Save, "🔖 Lưu", "Đã lưu", "save")},
	}}
}

var weekdays = []string{"Chủ nhật", "thứ Hai", "thứ Ba", "thứ Tư", "thứ Năm", "thứ Sáu", "thứ Bảy"}

// RenderHeader renders the digest header; day is the VN-local date.
func RenderHeader(day time.Time, total int, bySection map[string]int, hot int) string {
	var parts []string
	if hot > 0 {
		parts = append(parts, fmt.Sprintf("🔥 Nóng (%d)", hot))
	}
	for _, s := range []string{"ai", "tech", "dev", "vn"} {
		if n := bySection[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", SectionLabel(s), n))
		}
	}
	return fmt.Sprintf("☀️ <b>Bản tin %s %s — %d tin</b>\n%s", weekdays[day.Weekday()], day.Format("02/01"), total, Escape(strings.Join(parts, " · ")))
}
