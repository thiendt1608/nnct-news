package news

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"encore.dev/rlog"

	"encore.app/news/telegram"
)

const webhookPath = "/telegram/webhook"

// Webhook receives Telegram updates. Only requests carrying the secret token that setWebhook
// registered are accepted; updates from any chat other than TelegramChatID are ignored.
//
//encore:api public raw path=/telegram/webhook
func Webhook(w http.ResponseWriter, req *http.Request) {
	p, err := getPipeline()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	p.serveWebhook(w, req)
}

func (p *pipeline) serveWebhook(w http.ResponseWriter, req *http.Request) {
	got := req.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if p.webhookSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.webhookSecret)) != 1 {
		rlog.Warn("webhook: từ chối request sai secret", "header_present", got != "")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var u telegram.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&u); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := p.handleUpdate(req.Context(), u); err != nil {
		rlog.Error("webhook", "update_id", u.UpdateID, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError) // Telegram sẽ gửi lại; feedback idempotent
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (p *pipeline) handleUpdate(ctx context.Context, u telegram.Update) error {
	chat, ok := u.ChatID()
	if !ok || !telegram.AllowedChat(chat, p.chatID) {
		rlog.Info("webhook: bỏ qua update từ chat lạ", "update_id", u.UpdateID)
		return nil
	}
	if q := u.CallbackQuery; q != nil {
		return p.handleCallback(ctx, q)
	}
	if m := u.Message; m != nil {
		if cmd, arg, ok := telegram.ParseCommand(m.Text); ok {
			return p.handleCommand(ctx, cmd, arg)
		}
	}
	return nil
}

var feedbackAck = map[string]string{"up": "👍 Đã ghi nhận", "down": "👎 Đã ghi nhận", "save": "🔖 Đã lưu"}

func (p *pipeline) handleCallback(ctx context.Context, q *telegram.CallbackQuery) error {
	action, id, ok := telegram.ParseCallback(q.Data)
	if !ok {
		return p.tg.AnswerCallbackQuery(ctx, q.ID, "Nút không hợp lệ")
	}
	if action == "src" {
		if err := p.sendOtherSources(ctx, id); err != nil {
			return err
		}
		return p.tg.AnswerCallbackQuery(ctx, q.ID, "")
	}
	applied, err := p.recordFeedback(ctx, id, action)
	if err == errClusterNotFound {
		return p.tg.AnswerCallbackQuery(ctx, q.ID, "Tin này không còn trong dữ liệu")
	}
	if err != nil {
		return err
	}
	ack := feedbackAck[action]
	if !applied {
		ack = "Đã ghi nhận trước đó"
	}
	if err := p.tg.AnswerCallbackQuery(ctx, q.ID, ack); err != nil {
		rlog.Warn("answerCallbackQuery", "err", err.Error())
	}
	if q.Message == nil {
		return nil
	}
	_, btn, err := p.clusterLinks(ctx, id)
	if err != nil {
		return err
	}
	v, err := p.voted(ctx, id)
	if err != nil {
		return err
	}
	err = p.tg.EditMessageReplyMarkup(ctx, q.Message.MessageID, telegram.ItemMarkup(btn, id, v))
	if ae, ok := err.(*telegram.APIError); ok && strings.Contains(ae.Description, "message is not modified") {
		return nil
	}
	return err
}

// sendOtherSources replies with the links of every article of the cluster.
func (p *pipeline) sendOtherSources(ctx context.Context, clusterID int64) error {
	rows, err := db.Query(ctx, `SELECT i.source_id, i.title, i.url_canonical FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.cluster_id = $1 ORDER BY s.weight DESC, i.published_at ASC, i.id ASC LIMIT 10`, clusterID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString("📚 <b>Các bài trong cụm</b>\n")
	n := 0
	for rows.Next() {
		var sid, title, u string
		if err := rows.Scan(&sid, &title, &u); err != nil {
			return err
		}
		fmt.Fprintf(&b, "\n• %s — <a href=\"%s\">%s</a>", telegram.Escape(p.sourceName(sid)),
			strings.ReplaceAll(telegram.Escape(u), `"`, "&quot;"), telegram.Escape(title))
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err = p.tg.SendMessage(ctx, b.String(), nil)
	return err
}

// ensureWebhook registers the webhook once per process (idempotent on Telegram's side).
// Gọi từ cron ingest nên sau mỗi lần deploy tối đa ~2 giờ là có hiệu lực.
func (p *pipeline) ensureWebhook(ctx context.Context) {
	if p.publicBase == "" || p.webhookSecret == "" || p.webhookSet.Load() {
		return
	}
	if err := p.tg.SetWebhook(ctx, strings.TrimRight(p.publicBase, "/")+webhookPath, p.webhookSecret); err != nil {
		rlog.Warn("setWebhook", "err", err.Error())
		return
	}
	p.webhookSet.Store(true)
	rlog.Info("setWebhook ok")
}
