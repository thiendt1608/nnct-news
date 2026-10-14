# Spikes Phase 0

## T0.5 pgvector qua `sqldb` — ĐÃ KIỂM (2026-10-14)
- Truyền vector dạng text literal `'[…]'::halfvec` qua `sqldb` hoạt động: insert + truy vấn cosine `<=>` pass trong `encore test ./news/...` (`news/vec_test.go`). Không cần `pgvector-go`.
- pgvector local = **0.8.2** (đo từ test). AC2 trong PLAN ghi `0.7.0` là số cũ → chấp nhận ≥ 0.7.0. Encore Cloud chưa kiểm.

## T0.7 Kiểm nguồn — ĐÃ KIỂM (2026-10-14, `go run ./tools/check-sources`)
19/20 nguồn trả 200 + có item. `venturebeat-ai` trả 429 → `enabled: false`. Số item là đếm thô theo tag, chỉ để xác nhận feed sống. Điều khoản Reddit API: chưa kiểm (Reddit thuộc phase 2).

## T0.6 Gemini — chạy 2026-10-14 (`tools/spike-gemini`), một phần
Đã kiểm (model ID đúng, HTTP 200):
- `gemini-3.1-flash-lite` + `responseSchema`: OK, trả JSON đúng schema (context + key_points).
- `gemini-3.5-flash-lite` + `responseSchema`: OK, JSON đúng schema. Response có thêm `thoughtSignature` (model có thinking) → parser chỉ lấy `text`, bỏ qua field lạ.
- `gemini-embedding-2` + `outputDimensionality=768`: OK, 3 vector × 768 chiều.

Chưa kết luận:
- `gemma-4-31b-it` + schema: HTTP 500 INTERNAL. Có thể do lỗi tạm thời hoặc không hỗ trợ `responseSchema`; chưa phân biệt được. Cần chạy lại, thử không có schema (JSON trong prompt). Cho tới lúc đó coi Gemma **không** có structured output, validate JSON bằng code.
- Batch embed tính 1 hay N request: tool không đo được. Cách đo: xem số request `gemini-embedding-2` trong AI Studio → Usage trước/sau một lần chạy (đã gửi 1 batch gồm 3 text).
- Chất lượng tóm tắt 3.5 vs 3.1 FL trên 5 bài VN thật: chưa làm. Mới thấy trên 1 prompt ngắn: 3.1 viết đầy đủ hơn 3.5. Chưa đủ để kết luận.
