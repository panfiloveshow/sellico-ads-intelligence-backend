DROP INDEX IF EXISTS idx_keywords_workspace_normalized_no_cabinet;
DROP INDEX IF EXISTS idx_keywords_cabinet_normalized;
ALTER TABLE keywords ADD CONSTRAINT keywords_workspace_id_normalized_key UNIQUE (workspace_id, normalized);

DROP INDEX IF EXISTS idx_keyword_clusters_seller_cabinet;
DROP INDEX IF EXISTS idx_keywords_seller_cabinet;

ALTER TABLE keyword_clusters DROP COLUMN IF EXISTS seller_cabinet_id;
ALTER TABLE keywords DROP COLUMN IF EXISTS seller_cabinet_id;
