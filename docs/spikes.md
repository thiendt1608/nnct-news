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

Quyết định owner (2026-10-14):
- **Bỏ Gemma** khỏi kế hoạch (spike trả 500). Hết quota thì delay.
- Dùng **3.1 Flash-Lite** cho cả triage và tóm tắt (limit giống 3.5, rẻ hơn); 3.5 là fallback. Không cần so chất lượng tiếng Việt nữa.
- **Batch embed tính N request**: batch 3 text làm usage `gemini-embedding-2` tăng 3. Budget embed 800/ngày = tối đa ~800 item/ngày.
