# Changelog

## Unreleased
- feat: pipeline MVP — ingest RSS/HN (ETag, tự tắt nguồn lỗi), triage + embed + cluster (pgvector `halfvec`), hot score, chọn 20 tin có ràng buộc đa dạng, tóm tắt tiếng Việt, gửi Telegram (1 tin = 1 message, resume được), redirect `/r/:id` ghi click.
- feat: LLM client dùng chung — throttle theo request/token, retry 429/5xx, budget ngày lưu DB (giờ Pacific), fallback 3.1 → 3.5 Flash-Lite.
- feat: cron `ingest` mỗi 2 giờ, `digest` 06:xx/07:xx ICT.
- chore: bỏ Gemma khỏi kế hoạch; ghi nhận batch embed tính N request (docs/spikes.md).
- feat: khởi tạo app Encore, migration schema, `sources.yaml` ~20 nguồn, tool `check-sources`, `spike-gemini`.
