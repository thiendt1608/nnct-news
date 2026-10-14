# PLAN — nnct-news MVP

> Slug: `nnct-news-mvp` · Ngày lập: 2026-10-14 · Thiết kế gốc: [docs/DESIGN.md](../../../docs/DESIGN.md) v0.4
> Môi trường local (đã kiểm): Go 1.26.5, Encore CLI v1.57.9 (có bản vá bảo mật v1.58.6), Docker 29.8.2, git. Repo chưa `git init`.

## Goal

Bot Telegram cá nhân, mỗi sáng trước 8:00 ICT tự gửi 20 tin công nghệ/AI nóng nhất (VN + thế giới). Mỗi tin là 1 message có tóm tắt tiếng Việt dễ hiểu và nút mở bài gốc. Chạy hoàn toàn $0 trên Encore Cloud (Starter) + Gemini free tier.

## Non-goals

- UI web, nhiều user, public channel.
- X/Twitter, Facebook, scrape bất kỳ trang nào không có RSS/API.
- Đúng giờ tới từng phút (cron free tier random phút là chấp nhận được).
- Self-host trên AWS/GCP, gói trả phí ở Encore hoặc Google.

## Acceptance criteria

UAT chạy đúng các bước này. Phase 1 = MVP; phase 2 nằm cuối bảng.

| # | Tiêu chí | Cách kiểm |
|---|---|---|
| AC1 | Build + test xanh | `encore test ./...` pass; `go vet ./...` không cảnh báo |
| AC2 | Migration chạy, có pgvector | `encore db shell news` → `SELECT extversion FROM pg_extension WHERE extname='vector'` trả `0.7.0` |
| AC3 | Ingest chạy được và idempotent | gọi `news.Ingest` qua Local Development Dashboard → `SELECT state, count(*) FROM items GROUP BY 1` có `clustered > 0`; gọi lần 2 ngay sau đó không tạo item trùng (`count(*)` theo `url_canonical` không đổi) |
| AC4 | Hàng đợi chịu được 429 | test `llm` dùng fake transport trả 429 → client backoff, chuyển fallback model, không vượt `llm_usage` budget; item chưa xử lý vẫn ở state cũ |
| AC5 | Digest gửi đúng 1 header + 20 tin | gọi `news.Digest` → chat nhận 21 message silent; mỗi tin có nút `📖 Đọc bài gốc`; Telegram trả `ok:true` cho mọi message (không lỗi HTML parse) |
| AC6 | Digest idempotent + resume | gọi `news.Digest` lần 2 cùng ngày → không gửi thêm gì. Test: cắt job sau tin thứ 7 (inject lỗi) → lần chạy sau chỉ gửi 13 tin còn lại |
| AC7 | Ràng buộc đa dạng | unit test `select`: đúng 20 tin, ≤ 7/section, ≥ 3 tin VN (khi đủ ứng viên), ≤ 4/topic, không gửi lại cluster cũ trừ khi `n_sources` tăng ≥ 3 |
| AC8 | Tóm tắt đúng format | unit test render: mỗi message ≤ 4.096 ký tự, HTML escape đúng; tóm tắt có `context`, 3–5 `key_points`, `why_it_matters` |
| AC9 | Redirect an toàn | `GET /r/<id không tồn tại>` → 404; `GET /r/<id hợp lệ>` → 302 tới URL trong DB và ghi 1 dòng `feedback(kind='click')`; query param không đổi được đích redirect |
| AC10 | Chạy thật trên Encore Cloud | 7 ngày liên tiếp `SELECT digest_date, finished_at FROM digests` có `finished_at` < 08:00 ICT và đủ 20 tin `state='sent'` |
| AC11 | $0 | Google project của bot không gắn billing (Cloud Console → Billing: "This project has no billing account"); Encore app ở gói Starter |
| AC12 | Không lộ secret | `git grep -nE "AIza\|[0-9]{8,10}:[A-Za-z0-9_-]{35}"` rỗng; `.gitignore` có `.env*`; secrets chỉ nằm trong `encore secret` |
| AC13 (P2) | Webhook an toàn | request thiếu/sai header `X-Telegram-Bot-Api-Secret-Token` → 401; update từ chat khác `TelegramChatID` → bỏ qua |
| AC14 (P2) | Feedback hoạt động | bấm 👍/👎/🔖 → nút đổi thành ✅, có dòng `feedback`, `profile` topic weight thay đổi đúng ±0.05 / −0.1 |

## Tasks

Ký hiệu: **[O]** = owner tự làm (cần tài khoản/secret), **∥** = chạy song song được với các task ∥ khác cùng nhóm. Mỗi task code phải kèm test; commit kiểu conventional, cập nhật `CHANGELOG.md`.

### Phase 0 — Setup + spike (gỡ các điểm "not verified" trước khi code thật)

| ID | Task | File/module | Test / kết quả |
|---|---|---|---|
| T0.1 | Cập nhật Encore CLI lên bản vá bảo mật (`encore version update`) | — | `encore version` ≥ v1.58.6 |
| T0.2 | `git init`, tạo app Encore Go tại chỗ, `.gitignore` (`.env*`, `*.session`, `*.pkl`, `encore.gen.*` nếu cần), `README.md`, `CHANGELOG.md` | `encore.app`, `go.mod`, `.gitignore`, `README.md`, `CHANGELOG.md` | `encore run` khởi động OK |
| T0.3 **[O]** | Tạo bot qua @BotFather; tạo **Google project mới, không billing** + API key trong AI Studio; nhắn bot 1 tin rồi lấy `chat_id`; set 4 secret bằng `encore secret set --type dev,local,prod <Name>`: `TelegramBotToken`, `TelegramWebhookSecret` (chuỗi random), `TelegramChatID`, `GeminiAPIKey` | Encore secrets | `encore secret list` đủ 4 tên. Không paste giá trị vào chat/repo |
| T0.4 | Service `news` + DB + migration đầy đủ schema §8 DESIGN (bật `vector`, `halfvec(768)`, index HNSW `halfvec_cosine_ops`) | `news/service.go`, `news/db.go`, `news/migrations/1_init.up.sql` | AC2 |
| T0.5 ∥ | **Spike pgvector qua `sqldb`**: insert/query `halfvec` bằng `pgvector-go`; không được thì dùng text `'[…]'::halfvec` | `news/vec.go`, `news/vec_test.go` | `encore test ./news/...` có test round-trip vector + truy vấn cosine |
| T0.6 ∥ | **Spike Gemini** (chương trình Go riêng, không thuộc service): structured output với 3.1 FL và 3.5 FL; Gemma 4 31B có nhận JSON schema không; Embedding 2 có `output_dimensionality=768` không; batch embed tính 1 hay N request trên AI Studio; so chất lượng tóm tắt 3.5 vs 3.1 FL trên 5 bài VN thật | `tools/spike-gemini/main.go`, `docs/spikes.md` | `docs/spikes.md` ghi kết quả từng câu hỏi, cập nhật DESIGN nếu khác |
| T0.7 ∥ | **Kiểm nguồn**: `sources.yaml` ~20 nguồn; tool fetch từng feed, in số item + lỗi; loại feed chết; kiểm điều khoản Reddit API | `news/config/sources.yaml`, `tools/check-sources/main.go` | Bảng kết quả trong `docs/spikes.md`; mọi nguồn active trả ≥ 1 item |

Lưu ý: `//go:embed` không đọc được file ngoài thư mục package (không có `..`) → config đặt ở `news/config/`, khác với cây thư mục trong DESIGN §7.5.

### Phase 1 — MVP

| ID | Task | File/module | Test |
|---|---|---|---|
| T1.1 | Load config: sources, profile, model + limiter config (giá trị §9.3 DESIGN) | `news/config/*.yaml`, `news/config.go` | parse đủ field, lỗi rõ ràng khi thiếu |
| T1.2 ∥ | Fetchers RSS + Hacker News (Firebase API), User-Agent riêng, ETag/If-Modified-Since, timeout 10s, `errgroup.SetLimit(8)`, đếm lỗi → tự tắt nguồn | `news/ingest/rss.go`, `hn.go`, `fetch.go` | `httptest` server + fixture feed VN/EN; 304 không tạo item; lỗi 5 lần → `enabled=false` |
| T1.3 ∥ | Normalize + dedupe: canonical URL, title hash, bỏ item > 48h | `news/ingest/normalize.go`, `dedupe.go` | table test các kiểu URL (`utm_*`, `amp`, `m.`, trailing slash) |
| T1.4 ∥ | **LLM client**: `genai` wrapper; limiter request + token theo model; retry 429/5xx (backoff + jitter, tôn trọng `retryDelay`); budget ngày lưu `llm_usage` theo ngày giờ Pacific; fallback chain; structured output helper | `news/llm/client.go`, `limiter.go`, `budget.go` | AC4: fake `http.RoundTripper` trả 429/500/200; budget chạm trần → `ErrBudgetExhausted` |
| T1.5 | Triage: lấy `state='new'`, batch 40 (Gemma: 15), prompt + schema, cập nhật `triaged`/`dropped`, `attempts` | `news/brain/triage.go`, `prompts/triage.txt` | fake LLM; item `relevance<4`/`pr` → `dropped`; JSON hỏng → `attempts+1` |
| T1.6 | Embed + cluster: batch ≤ 80 text, text = title + 300 ký tự snippet; tìm cluster gần nhất 48h, cosine ≥ 0.82 (config); cập nhật centroid, `n_sources` | `news/brain/embed.go`, `cluster.go` | DB test: 2 tin cùng sự kiện → chung cluster; khác sự kiện → tách |
| T1.7 ∥ | Hot score (hàm thuần) + `hot_reason` | `news/brain/score.go` | table test từng thành phần, decay theo giờ |
| T1.8 | Endpoint + cron ingest: `//encore:api private func Ingest`, chạy fetch → dedupe → triage → embed → cluster → score trong budget; cron `Every: 2 * cron.Hour` | `news/ingest_job.go`, `news/cron.go` | AC3 |
| T1.9 ∥ | Chọn tin: 20 tin, ràng buộc đa dạng, luật gửi lại 🔄 | `news/brain/select.go` | AC7 |
| T1.10 ∥ | Trích bài gốc: `go-readability`, timeout, giới hạn 2 MB, cắt ~8K token, cờ `low_context` | `news/extract/extract.go` | fixture HTML VnExpress/TechCrunch (lưu sẵn), trang lỗi → `low_context` |
| T1.11 | Tóm tắt: prompt + schema §5.7 DESIGN, chỉ dùng text cung cấp, fallback model; lỗi hết đường → fallback title+snippet | `news/brain/summarize.go`, `prompts/summarize.txt` | fake LLM; schema thiếu field → retry/fallback |
| T1.12 ∥ | Telegram client + render: `sendMessage` (HTML, silent, tắt preview), `editMessageReplyMarkup`, `answerCallbackQuery`, `setWebhook`; tôn trọng `retry_after` khi 429; pacing ≥ 1,1s/message | `news/telegram/client.go`, `render.go` | AC8: golden test message, escape `<>&`, cắt `key_points` khi > 4.096 |
| T1.13 | Digest job: `digest_date` theo `Asia/Ho_Chi_Minh` (import `time/tzdata` phòng container thiếu zoneinfo); state `selected → summarized → sent`; header; resume; cron `Schedule: "0 23,0 * * *"` | `news/digest_job.go`, `news/cron.go` | AC5, AC6 |
| T1.14 | Redirect `/r/:id` (raw endpoint) + ghi click | `news/api_redirect.go` | AC9 |
| T1.15 | Deploy Encore Cloud (link app, `git push encore`), kiểm cron trên dashboard, gọi tay `Ingest` + `Digest` một lần | — | AC10 bắt đầu đếm; AC11, AC12 |

Thứ tự gợi ý: T1.1 → (T1.2 ∥ T1.3 ∥ T1.4 ∥ T1.7 ∥ T1.9 ∥ T1.10 ∥ T1.12) → T1.5 → T1.6 → T1.8 → T1.11 → T1.13 → T1.14 → T1.15.

### Phase 2 — Cá nhân hoá

| ID | Task | File/module | Test |
|---|---|---|---|
| T2.1 | Webhook raw endpoint: verify secret header, allowlist chat, router callback/command; `setWebhook` khi deploy | `news/api_bot.go`, `news/telegram/router.go` | AC13 |
| T2.2 | Callback 👍 👎 🔖 📚 + đổi nút ✅; học trọng số topic/source (kẹp [0.2, 2.0]) | `news/telegram/callbacks.go`, `news/brain/learn.go` | AC14 |
| T2.3 | Lệnh `/today` `/more` `/saved` `/mute` `/boost` `/status` `/search` | `news/telegram/commands.go` | test từng lệnh với fake client |
| T2.4 ∥ | Reddit fetcher (sau khi T0.7 xác nhận điều khoản) | `news/ingest/reddit.go` | fixture JSON |
| T2.5 ∥ | Cron cleanup: xoá item > 30 ngày, giữ cluster đã gửi + feedback; `/status` hiện `pg_database_size` | `news/cleanup_job.go` | DB test |
| T2.6 | Eval nhẹ: chấm 20 digest đầu, tune ngưỡng cluster, hệ số hot, prompt | `docs/eval.md` | ghi kết quả + thay đổi config |

### Phase 3 — Tuỳ chọn

Weekly recap Chủ nhật, newsletter qua email, Bluesky. Lập plan riêng khi cần.

## Risks

| Rủi ro | Ảnh hưởng | Giảm thiểu |
|---|---|---|
| `halfvec` không truyền thẳng qua `sqldb` | chặn T1.6 | Spike T0.5 trước; fallback cast từ text |
| Gemini đổi free tier / gỡ model / model không hỗ trợ structured output (Gemma) | pipeline đứng | Hàng đợi theo state, fallback chain, tên model + limit trong config; T0.6 kiểm Gemma |
| Embedding TPM 30K; batch có thể bị tính N request vào RPD | item kẹt ở `triaged` | Batch ≤ 80, chỉ embed item đã qua triage; T0.6 đo thật |
| Free tier: Google dùng dữ liệu để cải thiện sản phẩm | lộ dữ liệu nếu đưa nhầm | Prompt chỉ chứa tin công khai + profile chủ đề; không bao giờ có secret/data riêng |
| RSS VN chết, đổi URL, chặn bot; readability fail; paywall | thiếu tin VN, tóm tắt mỏng | T0.7 kiểm từng feed; auto-disable + cảnh báo; cờ `low_context` |
| Encore Cloud: cron random phút, timeout request chưa rõ, fair use (1 GB DB, 100K req/ngày), không có SLA | digest trễ/đứt giữa chừng | 2 lượt digest (06:xx, 07:xx) + resume theo từng tin; cleanup 30 ngày; `/status` theo dõi DB size |
| Container thiếu tzdata → tính sai `digest_date` | gửi trùng/thiếu ngày | `import _ "time/tzdata"`; unit test 23:30 UTC → ngày VN hôm sau |
| Telegram 429 khi gửi 21 message liền | thiếu tin | pacing ≥ 1,1s, tôn trọng `retry_after`, resume |
| Bản quyền nội dung | rủi ro pháp lý nếu public | Bot private, chỉ lưu tóm tắt + link; không đăng toàn văn |
| Điều khoản Reddit API | phải bỏ nguồn | Kiểm ở T0.7; Reddit để phase 2 |
| Rule 8 Sync / Anthropic | — | Không scrape X/FB; secret chỉ trong Encore secrets (AC12); không quyết định high-risk; conventional commits + CHANGELOG. Chạy `/sec` trước lần deploy đầu |
| Encore CLI v1.57.9 có bản vá bảo mật | lỗ hổng tool local | T0.1 |
| Deploy prod / push lên Encore | thao tác ra ngoài repo | Hỏi owner trước T1.15 |

## Assumptions

1. Slug `nnct-news-mvp`; repo chưa có `su-code/` nên plan nằm ở `.claude/ship/`.
2. Một service Encore duy nhất `news`, config embed ở `news/config/`.
3. Owner tự tạo bot, Google project không billing và set secrets (T0.3). Claude không đọc, in hay lưu giá trị secret.
4. Model + limit theo DESIGN §9 (ảnh chụp AI Studio ngày 2026-10-14): triage 3.1 Flash-Lite, tóm tắt 3.5 Flash-Lite, embedding Gemini Embedding 2 (768 chiều).
5. Ngưỡng ban đầu: cosine 0.82, relevance ≥ 4, 20 tin, ≥ 3 VN; tune ở T2.6.
6. Telegram gọi bằng `net/http` thuần, không thư viện bot.
7. MVP chưa có webhook: nút 👍 👎 🔖 vào ở phase 2, MVP chỉ có nút `📖` (URL button không cần webhook).
8. Không đặt mốc ngày; xong phase nào UAT phase đó theo bảng AC.
