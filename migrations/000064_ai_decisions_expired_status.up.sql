-- TTL на copilot-предложения: статус 'expired' для proposed-решений,
-- которые никто не подтвердил за 72 часа (ExpireStaleProposedAIDecisions).
-- Исходный CHECK из 000050 его не знал — UPDATE падал с 23514.
ALTER TABLE ai_decisions
    DROP CONSTRAINT IF EXISTS ai_decisions_status_check;
ALTER TABLE ai_decisions
    ADD CONSTRAINT ai_decisions_status_check CHECK (status IN (
        'shadow', 'proposed', 'approved', 'auto_applied', 'applied',
        'failed', 'rejected_by_user', 'rejected_by_guardrail', 'expired'));
