package news

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"encore.app/news/telegram"
)

const (
	listSize     = 5  // /more
	maxKeywords  = 50 // /mute + /boost mỗi loại
	maxKeywordLn = 60
)

const helpText = `<b>Lệnh</b>
/today — danh sách digest hôm nay
/more [ai|tech|dev|vn|chủ-đề] [trang] — 5 tin tiếp theo chưa gửi
/saved — tin đã 🔖
/search &lt;từ khoá&gt; — tìm trong 7 ngày gần nhất
/mute &lt;từ khoá&gt; · /unmute &lt;từ khoá&gt; — chặn tin có từ khoá trong tiêu đề
/boost &lt;từ khoá&gt; · /unboost &lt;từ khoá&gt; — ưu tiên tin có từ khoá
/status — nguồn, quota Gemini, dung lượng DB`

// handleCommand runs one bot command and replies in the chat.
func (p *pipeline) handleCommand(ctx context.Context, cmd, arg string) error {
	var text string
	var err error
	switch cmd {
	case "today":
		text, err = p.cmdToday(ctx)
	case "more":
		text, err = p.cmdMore(ctx, arg)
	case "saved":
		text, err = p.cmdSaved(ctx)
	case "search":
		text, err = p.cmdSearch(ctx, arg)
	case "mute", "unmute":
		text, err = p.cmdKeyword(ctx, "mute", "muted_extra", p.cfg.Profile.MutedKeywords, cmd == "unmute", arg)
	case "boost", "unboost":
		text, err = p.cmdKeyword(ctx, "boost", "boosted_extra", p.cfg.Profile.BoostedKeywords, cmd == "unboost", arg)
	case "status":
		text, err = p.cmdStatus(ctx)
	default: // /start, /help, lệnh lạ
		text = helpText
	}
	if err != nil {
		return err
	}
	_, err = p.tg.SendMessage(ctx, text, nil)
	return err
}

// clusterTitleSQL: tiêu đề tiếng Việt của bản tóm tắt gần nhất, không có thì tiêu đề bài mới nhất.
const clusterTitleSQL = `COALESCE(
	(SELECT d.summary->>'headline_vi' FROM digest_items d WHERE d.cluster_id = c.id AND d.summary IS NOT NULL ORDER BY d.digest_id DESC LIMIT 1),
	(SELECT i.title FROM items i WHERE i.cluster_id = c.id ORDER BY i.published_at DESC NULLS LAST, i.id DESC LIMIT 1), '')`

type listEntry struct {
	id      int64
	title   string
	section string
	nSrc    int
}

// renderList builds one message of numbered links (/r/:id) that stays under the Telegram limit.
func (p *pipeline) renderList(ctx context.Context, head string, es []listEntry) (string, error) {
	var b strings.Builder
	b.WriteString(head)
	for i, e := range es {
		_, u, err := p.clusterLinks(ctx, e.id)
		if err != nil {
			return "", err
		}
		line := fmt.Sprintf("\n%d. %s", i+1, linkHTML(u, e.title))
		if e.section != "" {
			line += " · " + telegram.SectionLabel(e.section)
		}
		if e.nSrc > 1 {
			line += fmt.Sprintf(" · %d nguồn", e.nSrc)
		}
		if utf8.RuneCountInString(b.String()+line) > telegram.MaxMessage-100 {
			fmt.Fprintf(&b, "\n… và %d tin nữa", len(es)-i)
			break
		}
		b.WriteString(line)
	}
	return b.String(), nil
}

func linkHTML(u, title string) string {
	return fmt.Sprintf(`<a href="%s">%s</a>`, strings.ReplaceAll(telegram.Escape(u), `"`, "&quot;"), telegram.Escape(title))
}

func (p *pipeline) queryList(ctx context.Context, q string, args ...any) ([]listEntry, error) {
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []listEntry
	for rows.Next() {
		var e listEntry
		if err := rows.Scan(&e.id, &e.title, &e.section, &e.nSrc); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// cmdToday lists the digest of today (VN date). Gửi lại 20 tin đầy đủ sẽ chạy hơn 20 giây
// trong một request webhook, nên chỉ liệt kê link; nút 👍👎 nằm ở tin gốc trong chat.
func (p *pipeline) cmdToday(ctx context.Context) (string, error) {
	day := vnDate(p.now()).Format("2006-01-02")
	es, err := p.queryList(ctx, `SELECT c.id, `+clusterTitleSQL+`, COALESCE(c.section,''), c.n_sources
		FROM digest_items di JOIN digests dg ON dg.id = di.digest_id JOIN clusters c ON c.id = di.cluster_id
		WHERE dg.digest_date = $1::date AND di.state = 'sent' ORDER BY di.rank`, day)
	if err != nil || len(es) == 0 {
		return "Hôm nay chưa có digest.", err
	}
	return p.renderList(ctx, fmt.Sprintf("📰 <b>Digest %s</b> (%d tin)\n", day, len(es)), es)
}

// cmdMore lists the next unsent clusters of a section or topic. Chưa tóm tắt (không tốn quota Gemini).
func (p *pipeline) cmdMore(ctx context.Context, arg string) (string, error) {
	f := strings.Fields(strings.ToLower(arg))
	page := 1
	if n := len(f); n > 0 {
		if v, err := strconv.Atoi(f[n-1]); err == nil && v >= 1 && v <= 50 {
			page, f = v, f[:n-1]
		}
	}
	filter := strings.Join(f, "-")
	since := p.now().Add(-time.Duration(p.cfg.Settings.ClusterWindowHrs) * time.Hour)
	es, err := p.queryList(ctx, `SELECT c.id, `+clusterTitleSQL+`, COALESCE(c.section,''), c.n_sources FROM clusters c
		WHERE c.section IS NOT NULL AND c.n_sources > 0 AND c.first_seen > $1
		  AND NOT EXISTS (SELECT 1 FROM digest_items d WHERE d.cluster_id = c.id)
		  AND ($2 = '' OR c.section = $2 OR $2 = ANY(c.topics))
		ORDER BY c.hot_score DESC, c.id LIMIT $3 OFFSET $4`, since, filter, listSize, (page-1)*listSize)
	if err != nil {
		return "", err
	}
	if len(es) == 0 {
		return "Hết tin chưa gửi" + inFilter(filter) + ".", nil
	}
	head := fmt.Sprintf("➕ <b>Thêm tin%s</b> (trang %d, chưa tóm tắt)\n", inFilter(filter), page)
	return p.renderList(ctx, head, es)
}

func inFilter(f string) string {
	if f == "" {
		return ""
	}
	return " " + telegram.Escape(f)
}

func (p *pipeline) cmdSaved(ctx context.Context) (string, error) {
	es, err := p.queryList(ctx, `SELECT c.id, `+clusterTitleSQL+`, COALESCE(c.section,''), 0
		FROM (SELECT cluster_id, max(at) AS at FROM feedback WHERE kind = 'save' GROUP BY cluster_id) f
		JOIN clusters c ON c.id = f.cluster_id ORDER BY f.at DESC LIMIT 20`)
	if err != nil || len(es) == 0 {
		return "Chưa lưu tin nào. Bấm 🔖 dưới tin để lưu.", err
	}
	return p.renderList(ctx, "🔖 <b>Tin đã lưu</b>\n", es)
}

// cmdSearch matches item titles of the last 7 days (kể cả bài chưa vào digest).
func (p *pipeline) cmdSearch(ctx context.Context, arg string) (string, error) {
	if arg == "" {
		return "Dùng: /search &lt;từ khoá&gt;", nil
	}
	if utf8.RuneCountInString(arg) > 100 {
		return "Từ khoá dài quá (tối đa 100 ký tự).", nil
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(arg) + "%"
	rows, err := db.Query(ctx, `SELECT i.source_id, i.title, i.url_canonical FROM items i
		WHERE i.state <> 'dropped' AND COALESCE(i.published_at, i.fetched_at) > $1 AND i.title ILIKE $2 ESCAPE '\'
		ORDER BY COALESCE(i.published_at, i.fetched_at) DESC LIMIT 10`, p.now().AddDate(0, 0, -7), like)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "🔎 <b>%s</b>\n", telegram.Escape(arg))
	n := 0
	for rows.Next() {
		var sid, title, u string
		if err := rows.Scan(&sid, &title, &u); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n• %s — %s", telegram.Escape(p.sourceName(sid)), linkHTML(u, title))
		n++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n == 0 {
		return "Không thấy bài nào khớp \"" + telegram.Escape(arg) + "\" trong 7 ngày gần nhất.", nil
	}
	return b.String(), nil
}

// extraKeywords reads the keywords added through /mute and /boost.
func (p *pipeline) extraKeywords(ctx context.Context, key string) ([]string, error) {
	var raw string
	if err := db.QueryRow(ctx, `SELECT value::text FROM profile WHERE key = $1`, key).Scan(&raw); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	var kw []string
	if err := json.Unmarshal([]byte(raw), &kw); err != nil {
		return nil, fmt.Errorf("profile.%s hỏng: %w", key, err)
	}
	return kw, nil
}

// keywords returns the muted and boosted keywords: profile.yaml plus what /mute and /boost added.
func (p *pipeline) keywords(ctx context.Context) (muted, boosted []string, err error) {
	mx, err := p.extraKeywords(ctx, "muted_extra")
	if err != nil {
		return nil, nil, err
	}
	bx, err := p.extraKeywords(ctx, "boosted_extra")
	if err != nil {
		return nil, nil, err
	}
	return append(append([]string(nil), p.cfg.Profile.MutedKeywords...), mx...),
		append(append([]string(nil), p.cfg.Profile.BoostedKeywords...), bx...), nil
}

// cmdKeyword adds or removes a keyword in profile.<key>; không đụng danh sách trong profile.yaml.
func (p *pipeline) cmdKeyword(ctx context.Context, name, key string, base []string, remove bool, arg string) (string, error) {
	kw := strings.ToLower(strings.Join(strings.Fields(arg), " "))
	cur, err := p.extraKeywords(ctx, key)
	if err != nil {
		return "", err
	}
	list := func() string {
		out := "mặc định: " + strings.Join(base, ", ")
		if len(cur) > 0 {
			out += "\nbạn thêm: " + strings.Join(cur, ", ")
		}
		return telegram.Escape(out)
	}
	if kw == "" {
		return "Dùng: /" + name + " &lt;từ khoá&gt;\n" + list(), nil
	}
	if utf8.RuneCountInString(kw) > maxKeywordLn {
		return "Từ khoá dài quá (tối đa 60 ký tự).", nil
	}
	if remove {
		if _, err := db.Exec(ctx, `UPDATE profile SET value = value - $2::text WHERE key = $1`, key, kw); err != nil {
			return "", err
		}
		return "Đã bỏ \"" + telegram.Escape(kw) + "\" (từ khoá trong profile.yaml không xoá được từ bot).", nil
	}
	if len(cur) >= maxKeywords {
		return "Đã đủ " + strconv.Itoa(maxKeywords) + " từ khoá, bỏ bớt bằng /un" + key[:5] + ".", nil
	}
	if _, err := db.Exec(ctx, `INSERT INTO profile (key, value) VALUES ($1, to_jsonb(ARRAY[$2::text]))
		ON CONFLICT (key) DO UPDATE SET value = CASE WHEN profile.value @> to_jsonb($2::text) THEN profile.value
			ELSE profile.value || to_jsonb($2::text) END`, key, kw); err != nil {
		return "", err
	}
	return "Đã thêm \"" + telegram.Escape(kw) + "\". Có hiệu lực từ lượt ingest sau.", nil
}

func (p *pipeline) cmdStatus(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString("📊 <b>Trạng thái</b>\n")

	var enabled, off int
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE enabled), count(*) FILTER (WHERE NOT enabled) FROM sources`).Scan(&enabled, &off); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\nNguồn: %d bật, %d tắt", enabled, off)
	rows, err := db.Query(ctx, `SELECT id, fail_count, enabled FROM sources WHERE fail_count > 0 OR NOT enabled ORDER BY fail_count DESC, id LIMIT 15`)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var id string
		var fails int
		var en bool
		if err := rows.Scan(&id, &fails, &en); err != nil {
			rows.Close()
			return "", err
		}
		state := fmt.Sprintf("lỗi %d lần", fails)
		if !en {
			state = "đang tắt"
		}
		fmt.Fprintf(&b, "\n• %s: %s", telegram.Escape(p.sourceName(id)), state)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}

	b.WriteString("\n\nGemini hôm nay (đã dùng/quota):")
	for _, name := range sortedKeys(p.cfg.Settings.Models) {
		rem, err := p.llm.Remaining(ctx, name)
		if err != nil {
			return "", err
		}
		budget := p.cfg.Settings.Models[name].DailyBudget
		fmt.Fprintf(&b, "\n• %s: %d/%d", telegram.Escape(name), budget-rem, budget)
	}

	var items, clusters int
	var size int64
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM items), (SELECT count(*) FROM clusters), pg_database_size(current_database())`).Scan(&items, &clusters, &size); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\n\nDB: %.1f MB · %d bài · %d cụm", float64(size)/(1<<20), items, clusters)
	return b.String(), nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
