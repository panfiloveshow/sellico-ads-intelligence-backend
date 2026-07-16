ALTER TABLE bid_decision_observations
    DROP CONSTRAINT IF EXISTS bid_decision_observations_automation_level_check;
ALTER TABLE bid_decision_observations
    ADD CONSTRAINT bid_decision_observations_automation_level_check
    CHECK (automation_level IN (1, 2)) NOT VALID;

DROP TABLE IF EXISTS strategy_evaluation_facts;
DROP TABLE IF EXISTS strategy_evaluation_runs;
DROP TABLE IF EXISTS strategy_binding_rollouts;
