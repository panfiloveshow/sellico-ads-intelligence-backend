-- Возврат исходного списка статусов (000050). Строки со статусом 'expired'
-- перед этим переводятся в rejected_by_user, иначе ADD CONSTRAINT упадёт.
UPDATE ai_decisions SET status = 'rejected_by_user' WHERE status = 'expired';
ALTER TABLE ai_decisions
    DROP CONSTRAINT IF EXISTS ai_decisions_status_check;
ALTER TABLE ai_decisions
    ADD CONSTRAINT ai_decisions_status_check CHECK (status IN (
        'shadow', 'proposed', 'approved', 'auto_applied', 'applied',
        'failed', 'rejected_by_user', 'rejected_by_guardrail'));
