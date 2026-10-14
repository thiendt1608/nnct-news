# nnct-news

Bot Telegram cá nhân: mỗi sáng gửi **20 tin công nghệ/AI nóng nhất**, tóm tắt tiếng Việt, có sẵn trước 8:00 ICT. Chi phí mục tiêu: **$0** (Gemini free tier, Encore free tier).

## Cách hoạt động

```
RSS / Hacker News ──► ingest (2h/lần) ──► triage ──► embed ──► cluster theo sự kiện ──► hot score
                                                                                        │
Telegram (1 tin = 1 message) ◄── tóm tắt VN ◄── fetch full text ◄── chọn top 20 (đa dạng) ◄┘
```

- **Ingest**: RSS + HN, hỗ trợ ETag, tự tắt nguồn lỗi (`news/config/sources.yaml`).
- **Triage + embed + cluster**: Gemini lọc tin loãng, gom các bài cùng sự kiện bằng pgvector (`halfvec`).
- **Hot score**: số nguồn độc lập (không tính aggregator) + engagement.
- **Digest**: chọn 20 tin có ràng buộc đa dạng chủ đề, tóm tắt bám sát bài gốc (không bịa số liệu), gửi Telegram, resume được nếu đứt giữa chừng.
- **Redirect** `/r/:id` ghi lại click để học sở thích.
- LLM client dùng chung: throttle theo request/token, retry, budget ngày, fallback 3.1 → 3.5 Flash-Lite.

## Stack

Go · [Encore](https://encore.dev) · Postgres + pgvector · Gemini API · Telegram Bot API

## Dev

```bash
encore run                     # chạy local (cần Docker cho Postgres)
encore test ./...              # test, gồm pgvector/halfvec
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

## Tài liệu

- [docs/DESIGN.md](docs/DESIGN.md) — thiết kế
- [docs/spikes.md](docs/spikes.md) — kết quả spike Gemini
- [CHANGELOG.md](CHANGELOG.md)
