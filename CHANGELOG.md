# Changelog

## Unreleased
- feat: lệnh bot (T2.3) — `/today` `/more [section|chủ đề] [trang]` `/saved` `/search` `/mute` `/unmute` `/boost` `/unboost` `/status` `/help`. `/today` và `/more` trả danh sách link (không gửi lại tin đầy đủ, `/more` chưa tóm tắt nên không tốn quota Gemini). Từ khoá thêm từ bot lưu ở `profile.muted_extra`/`boosted_extra`, gộp với `profile.yaml` ở triage và hot score.
- feat: cron `cleanup` mỗi ngày (T2.5) — xoá bài/cluster cũ hơn `retention_days` (30) trừ cluster đã gửi hoặc có feedback; bài giữ lại bỏ embedding, cluster cũ bỏ centroid; xoá `llm_usage` quá hạn; log `pg_database_size`.
- fix: log request webhook bị từ chối; secret `TelegramWebhookSecret` phải thuộc `A-Za-z0-9_-` (Telegram từ chối ký tự khác khi `setWebhook`).
- feat: webhook Telegram `/telegram/webhook` (T2.1) — bắt buộc header `X-Telegram-Bot-Api-Secret-Token` (sai/thiếu → 401), chỉ xử lý update từ `TelegramChatID`, tự `setWebhook` từ cron ingest.
- feat: nút 👍 👎 🔖 📚 dưới mỗi tin (T2.2) — bấm đổi thành ✅, mỗi tin một phiếu 👍/👎 và một 🔖 (bấm lại không cộng dồn); 👍/🔖 cộng +0.05 topic / +0.02 nguồn, 👎 trừ 0.10 / 0.04, kẹp [0.2, 2.0]; nguồn aggregator không bị học. Trọng số học được lưu ở `profile.topic_adj` và `sources.weight_adj` (không bị `sources.yaml` ghi đè) và dùng ở lần tính hot score kế tiếp. Migration `2_feedback`.
- feat: mở rộng danh sách chủ đề triage (agents, research, backend, cloud, architecture, system-design, software-engineering, algorithms, technology) kèm định nghĩa trong prompt; giữ nguyên `vn-tech-startup`.
- fix: section `vn` chỉ cho tin gắn chủ đề `vn-tech-startup`; báo VN đưa tin quốc tế (OpenAI, chip AI...) theo chủ đề thay vì bị gắn VN (11/120 tin chấm sai section, 7 ca do lỗi này).
- fix: section của cluster theo chủ đề chính (không theo nguồn).
- fix: aggregator (Hacker News, Lobsters) không tính vào `n_sources` và hot score.
- fix: prompt tóm tắt chặt hơn (không bịa số liệu/thông số, ghi rõ cáo buộc/tự công bố, ngắn hơn ~150 từ, glossary chỉ thuật ngữ kỹ thuật).
- feat: pipeline MVP — ingest RSS/HN (ETag, tự tắt nguồn lỗi), triage + embed + cluster (pgvector `halfvec`), hot score, chọn 20 tin có ràng buộc đa dạng, tóm tắt tiếng Việt, gửi Telegram (1 tin = 1 message, resume được), redirect `/r/:id` ghi click.
- feat: LLM client dùng chung — throttle theo request/token, retry 429/5xx, budget ngày lưu DB (giờ Pacific), fallback 3.1 → 3.5 Flash-Lite.
- feat: cron `ingest` mỗi 2 giờ, `digest` 06:xx/07:xx ICT.
- chore: bỏ Gemma khỏi kế hoạch; ghi nhận batch embed tính N request (docs/spikes.md).
- feat: khởi tạo app Encore, migration schema, `sources.yaml` ~20 nguồn, tool `check-sources`, `spike-gemini`.
