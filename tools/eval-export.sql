-- Xuất digest mới nhất thành bảng markdown để chấm (docs/eval.md).
-- Chạy: encore db shell news --env=staging < tools/eval-export.sql
-- Đổi ngày: sửa subquery digest_date bên dưới.
\pset format unaligned
\pset tuples_only on
\pset footer off
WITH d AS (SELECT id, digest_date FROM digests ORDER BY digest_date DESC LIMIT 1)
SELECT '### ' || d.digest_date || E'\n\n'
    || '| # | Tiêu đề (VI) | Section (bot gắn) | Nguồn | Cụm | Fb | Click | Tóm tắt | Đáng đọc | Trùng | Section đúng | Cụm sai | Ghi chú |' || E'\n'
    || '|---|---|---|---|---|---|---|---|---|---|---|---|---|'
FROM d
UNION ALL
SELECT * FROM (
  SELECT '| ' || di.rank
      || ' | ' || replace(coalesce(di.summary->>'headline_vi', '(chưa có)'), '|', '/')
      || ' | ' || coalesce(c.section, '?')
      || ' | ' || coalesce((SELECT string_agg(DISTINCT i.source_id, ', ') FROM items i WHERE i.cluster_id = c.id), '?')
      || ' | ' || c.n_sources
      || ' | ' || CASE WHEN di.fallback THEN 'fb' ELSE '' END
      || ' | ' || (SELECT count(*) FROM feedback f WHERE f.cluster_id = c.id AND f.kind = 'click')
      || ' | | | | | | |'
  FROM d
  JOIN digest_items di ON di.digest_id = d.id
  JOIN clusters c ON c.id = di.cluster_id
  ORDER BY di.rank
) t;
