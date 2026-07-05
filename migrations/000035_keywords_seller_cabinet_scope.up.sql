-- Keywords and their clusters were workspace-scoped only, so a workspace with
-- multiple seller cabinets (stores in different niches) saw every cabinet's ad
-- keywords blended into one pool. Scope both tables to a seller cabinet.
--
-- Existing rows keep seller_cabinet_id = NULL: we cannot retroactively tell
-- which cabinet a blended-in legacy keyword came from. They become inert
-- (cabinet-scoped queries never return NULL-cabinet rows) rather than being
-- deleted; a fresh "Собрать" re-collects real per-cabinet data.
ALTER TABLE keywords ADD COLUMN IF NOT EXISTS seller_cabinet_id UUID NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE;
ALTER TABLE keyword_clusters ADD COLUMN IF NOT EXISTS seller_cabinet_id UUID NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_keywords_seller_cabinet ON keywords (seller_cabinet_id);
CREATE INDEX IF NOT EXISTS idx_keyword_clusters_seller_cabinet ON keyword_clusters (seller_cabinet_id);

-- The old workspace-wide uniqueness let two cabinets' identical query text
-- collide into a single row. Re-scope uniqueness to the cabinet.
ALTER TABLE keywords DROP CONSTRAINT IF EXISTS keywords_workspace_id_normalized_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_keywords_cabinet_normalized ON keywords (seller_cabinet_id, normalized);

-- SERP-sourced keywords are market research, not tied to any one store, so
-- they keep seller_cabinet_id NULL. A plain unique index never matches NULL
-- in an ON CONFLICT target, so give this source its own dedup key —
-- otherwise every sweep would insert a fresh duplicate row instead of
-- updating frequency on the existing one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_keywords_workspace_normalized_no_cabinet
    ON keywords (workspace_id, normalized) WHERE seller_cabinet_id IS NULL;
