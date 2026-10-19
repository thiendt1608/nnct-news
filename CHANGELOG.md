# Changelog

## Unreleased
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
