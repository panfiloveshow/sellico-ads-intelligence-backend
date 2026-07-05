-- Freeze switch: while repricer_paused_until is in the future, the repricer
-- keeps computing recommendations for the cabinet but applies nothing to WB.
ALTER TABLE seller_cabinets
    ADD COLUMN repricer_paused_until TIMESTAMPTZ NULL;
