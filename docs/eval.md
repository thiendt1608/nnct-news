# Eval nhẹ — chấm digest hằng ngày

Mục tiêu (T2.6): có số liệu để tune ngưỡng cluster, hệ số hot và prompt, thay vì đoán. Chấm 5–7 ngày đầu rồi mới tune; một ngày thì chưa đủ mẫu.

## Cách chấm (~3 phút/ngày)

Với mỗi tin trong digest, điền 1 dòng trong bảng của ngày đó.

**Tóm tắt** (so với bài gốc, mở link `📖` nếu nghi ngờ):

| Nhãn | Nghĩa |
|---|---|
| `ok` | đúng và đủ ý chính |
| `sai` | có số liệu/ý sai hoặc bịa (ghi rõ chỗ sai ở cột Ghi chú) |
| `thieu` | bỏ sót ý chính, đọc xong chưa hình dung được |
| `kho` | đúng nhưng khó hiểu, dài dòng hoặc glossary thừa |
| `fb` | bị fallback (⚠️ chỉ có trích đoạn) |

**Tin** (chọn đúng tin chưa?):

| Cột | Giá trị |
|---|---|
| Đáng đọc | `Y` đáng đọc / `N` nhạt, không muốn nhận loại này |
| Trùng | `Y` nếu cùng sự kiện với tin khác trong ngày (cluster không gom) |
| Section | `Y` nếu nhãn section đúng, `N` nếu sai (vd tin AI gắn Tech) |
| Cụm sai | `Y` nếu cluster gom hai sự kiện khác nhau (nhiều nguồn nhưng nội dung lệch) |

Ghi chú ngắn gọn: nguồn nào hay lỗi, từ khoá nên mute/boost, số nào bịa.

## Xuất bảng tự động (khỏi gõ tay)

Bảng đã điền sẵn tiêu đề, section bot gắn, nguồn, số nguồn, fallback, số click; chỉ còn các cột chấm.

```bash
encore db shell news --env=staging < tools/eval-export.sql | pbcopy
```

Cần `psql` trên máy (`brew install libpq && brew link --force libpq`). Dán kết quả vào "Nhật ký chấm" rồi điền cột Tóm tắt / Đáng đọc / Trùng / Section đúng / Cụm sai.

## Mẫu bảng một ngày (nếu điền tay)

Sao chép khối này cho mỗi ngày, đổi ngày.

### 2026-MM-DD

| # | Tiêu đề (rút gọn) | Tóm tắt | Đáng đọc | Trùng | Section | Cụm sai | Ghi chú |
|---|---|---|---|---|---|---|---|
| 1 | | | | | | | |
| 2 | | | | | | | |
| 3 | | | | | | | |
| 4 | | | | | | | |
| 5 | | | | | | | |
| 6 | | | | | | | |
| 7 | | | | | | | |
| 8 | | | | | | | |
| 9 | | | | | | | |
| 10 | | | | | | | |
| 11 | | | | | | | |
| 12 | | | | | | | |
| 13 | | | | | | | |
| 14 | | | | | | | |
| 15 | | | | | | | |
| 16 | | | | | | | |
| 17 | | | | | | | |
| 18 | | | | | | | |
| 19 | | | | | | | |
| 20 | | | | | | | |

Tin quan trọng bị **bỏ sót** (có trên báo nhưng không vào digest): 

Tổng kết ngày: ok __/20 · sai __ · thieu __ · kho __ · fb __ · đáng đọc N __ · trùng __ · section sai __ · cụm sai __

## Chỉ số theo dõi (điền sau mỗi 5–7 ngày)

| Chỉ số | Cách tính | Mục tiêu | Giá trị |
|---|---|---|---|
| Tỉ lệ tin không đáng đọc | tổng `N` ở cột Đáng đọc / tổng tin | < 20% (R2 trong DESIGN) | |
| Tỉ lệ tóm tắt sai | tổng `sai` / tổng tin | gần 0; mỗi ca `sai` đều phải điều tra | |
| Tỉ lệ fallback | tổng `fb` / tổng tin | < 5% | |
| Tin trùng sự kiện | tổng `Y` cột Trùng / số ngày | ≤ 1/ngày | |
| Cluster gom sai | tổng `Y` cột Cụm sai / tổng cluster nhiều nguồn | ≈ 0 | |
| Section sai | tổng `N` cột Section / tổng tin | < 5% | |
| Tin bỏ sót quan trọng | đếm từ dòng "bỏ sót" | ≤ 1/ngày | |

## Từ triệu chứng đến chỗ chỉnh

Chỉnh **một thứ mỗi lần** rồi chấm tiếp vài ngày để thấy tác dụng. Ghi mỗi lần chỉnh vào bảng "Nhật ký tune" bên dưới.

| Triệu chứng | Chỉnh ở đâu |
|---|---|
| Tin trùng sự kiện nhưng không gom | giảm `cluster_threshold` (0.82 → 0.78) trong `news/config/settings.yaml` |
| Hai sự kiện khác nhau bị gom chung | tăng `cluster_threshold` |
| Nhiều tin nhạt lọt vào | tăng `min_relevance` (4 → 5), thêm `muted_keywords` trong `profile.yaml` |
| Hay bỏ sót tin quan trọng | giảm `min_relevance`, thêm `boosted_keywords`, tăng weight topic trong `profile.yaml` |
| Tin nóng không lên top | chỉnh hệ số trong `news/brain/score.go` (xem `hot_reason` trong DB để biết thành phần nào kéo) |
| Tóm tắt bịa/dài/khó hiểu | chỉnh `news/brain/prompts/summarize.txt` |
| Thiếu tin VN hoặc lệch section | `min_vn`, `max_per_section`, `max_per_topic` trong `settings.yaml` |
| Một nguồn hay ra tin rác | giảm `weight` của nguồn trong `sources.yaml`, hoặc `enabled: false` |

Xem lý do một tin lên top:

```sql
SELECT di.rank, c.section, c.n_sources, c.hot_reason
FROM digest_items di JOIN clusters c ON c.id = di.cluster_id
WHERE di.digest_id = (SELECT id FROM digests ORDER BY digest_date DESC LIMIT 1)
ORDER BY di.rank;
```

## Nhật ký tune

| Ngày | Triệu chứng | Thay đổi (file: giá trị cũ → mới) | Kết quả sau vài ngày |
|---|---|---|---|
| 2026-10-14 | #1 OpenAI gắn nhầm Tech; HN/Lobsters thổi phồng `n_sources`; tóm tắt có số liệu không có trong bài, dài | `cluster.go`: section theo chủ đề; `sources.yaml`: `aggregator: true` cho HN/Lobsters; `summarize.txt` siết chống bịa số | thử trên 3 bài: số bịa biến mất; chưa đo trên digest thật |
| 2026-10-19 | Section sai 11/120 (9%, mục tiêu <5%): 7 ca tin quốc tế từ báo VN bị gắn `vn`; chủ đề quá hẹp (không có backend/cloud/agents...) | `triage.txt`: thêm 11 chủ đề + định nghĩa; `profile.yaml`: trọng số cho chủ đề mới; `section.go`: `vn` chỉ khi có chủ đề `vn` | chưa đo |

## Nhật ký chấm

Dán các bảng ngày bên dưới, ngày mới nhất ở trên cùng.
