# nnct-news

Bot Telegram cá nhân: mỗi sáng gửi 20 tin công nghệ/AI nóng nhất, tóm tắt tiếng Việt. Thiết kế: [docs/DESIGN.md](docs/DESIGN.md). Kế hoạch: `.claude/ship/nnct-news-mvp/PLAN.md`.

## Dev

```bash
encore run          # chạy local (cần Docker cho Postgres)
encore test ./...   # test, gồm pgvector/halfvec
go run ./tools/check-sources   # kiểm các feed trong news/config/sources.yaml
```

## Secrets (chỉ qua Encore, không commit)

```bash
encore secret set --type dev,local,prod TelegramBotToken
encore secret set --type dev,local,prod TelegramWebhookSecret
encore secret set --type dev,local,prod TelegramChatID
encore secret set --type dev,local,prod GeminiAPIKey
```

Spike Gemini đọc key từ biến môi trường `GEMINI_API_KEY` (đặt trong shell, không ghi vào file).
