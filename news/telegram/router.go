package telegram

import (
	"strconv"
	"strings"
)

// Update is the subset of a Bot API update the bot reacts to.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// ChatID returns the chat an update came from; ok is false when it has none we can attribute.
func (u Update) ChatID() (int64, bool) {
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		return u.CallbackQuery.Message.Chat.ID, true
	case u.Message != nil:
		return u.Message.Chat.ID, true
	}
	return 0, false
}

// AllowedChat reports whether chatID is the configured owner chat (decimal id).
func AllowedChat(chatID int64, configured string) bool {
	id, err := strconv.ParseInt(strings.TrimSpace(configured), 10, 64)
	return err == nil && id == chatID
}

// ParseCallback splits callback_data "action:clusterID".
func ParseCallback(data string) (action string, clusterID int64, ok bool) {
	a, rest, found := strings.Cut(data, ":")
	if !found {
		return "", 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	switch a {
	case "up", "down", "save", "src":
		return a, id, true
	}
	return "", 0, false
}

// ParseCommand splits "/cmd@bot arg…" into a lowercase command and its trimmed argument.
// ok is false for text that is not a bot command.
func ParseCommand(text string) (cmd, arg string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || len(text) < 2 {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	if head == "" {
		return "", "", false
	}
	return strings.ToLower(head), strings.TrimSpace(rest), true
}
