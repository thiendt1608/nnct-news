# Spikes Phase 0

## T0.5 pgvector qua `sqldb` — ĐÃ KIỂM (2026-10-14)
- Truyền vector dạng text literal `'[…]'::halfvec` qua `sqldb` hoạt động: insert + truy vấn cosine `<=>` pass trong `encore test ./news/...` (`news/vec_test.go`). Không cần `pgvector-go`.
- pgvector local = **0.8.2** (đo từ test). AC2 trong PLAN ghi `0.7.0` là số cũ → chấp nhận ≥ 0.7.0. Encore Cloud chưa kiểm.

## T0.7 Kiểm nguồn — ĐÃ KIỂM (2026-10-14, `go run ./tools/check-sources`)
19/20 nguồn trả 200 + có item. `venturebeat-ai` trả 429 → `enabled: false`. Số item là đếm thô theo tag, chỉ để xác nhận feed sống. Điều khoản Reddit API: chưa kiểm (Reddit thuộc phase 2).

## T0.6 Gemini — CHƯA CHẠY
Cần `GEMINI_API_KEY` từ project Google không billing. Chạy `GEMINI_API_KEY=… go run ./tools/spike-gemini`, rồi ghi kết quả vào đây:
- Structured output: 3.1 FL / 3.5 FL / Gemma 4 31B?
- Embedding 2 có `outputDimensionality=768`?
- Batch embed tính 1 hay N request (xem usage AI Studio)?
- So chất lượng tóm tắt 3.5 vs 3.1 FL trên 5 bài VN (cần làm tay/bổ sung tool).
Model ID trong tool là đoán theo DESIGN — not verified, sửa theo danh sách model thật.
