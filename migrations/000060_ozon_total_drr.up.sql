-- Ozon module: «ДРР от общего оборота» — the second ДРР, measured against the
-- cabinet's whole turnover (ozon_sales_daily) rather than the ad-attributed
-- revenue of a campaign (ozon_campaign_stats).
--
-- Stage 0 is observation only: the numbers are recorded next to the existing
-- campaign-ДРР ones and fed back to the AI, but nothing acts on them yet.
--
-- Scope is the CABINET, deliberately: a SKU can sit in several campaigns, so a
-- per-campaign split of the total turnover needs an attribution rule that does
-- not exist yet. Cabinet scope has no double counting at all.
ALTER TABLE ai_decisions
    ADD COLUMN total_drr_before NUMERIC(8,2),
    ADD COLUMN total_drr_after  NUMERIC(8,2);

COMMENT ON COLUMN ai_decisions.total_drr_before IS
    'Cabinet-wide ДРР (ad spend / total turnover × 100) over the before-window; NULL when the turnover is unknown or zero';
COMMENT ON COLUMN ai_decisions.total_drr_after IS
    'Cabinet-wide ДРР over the after-window; NULL when the turnover is unknown or zero';
