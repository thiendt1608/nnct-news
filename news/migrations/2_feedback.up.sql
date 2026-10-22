-- Phase 2: học sở thích từ 👍 👎 🔖. Mỗi cluster chỉ tính một lần cho mỗi loại để bấm lại không cộng dồn.
ALTER TABLE sources ADD COLUMN weight_adj real NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX feedback_one_vote ON feedback (cluster_id) WHERE kind IN ('up', 'down');
CREATE UNIQUE INDEX feedback_one_save ON feedback (cluster_id) WHERE kind = 'save';
