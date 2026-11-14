-- A downgrade must not discard review receipts or resurrect unresolved writes.
-- Older code keeps all admissions fenced. Retain this additive audit table.
SELECT 1;
