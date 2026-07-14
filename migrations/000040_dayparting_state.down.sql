DROP TABLE IF EXISTS dayparting_states;
DROP TRIGGER IF EXISTS strategy_bindings_scope_guard ON strategy_bindings;
DROP FUNCTION IF EXISTS validate_strategy_binding_scope();
DROP INDEX IF EXISTS wb_bid_actions_automation_key_unique;
DROP INDEX IF EXISTS wb_bid_actions_automation_observation_key_unique;
ALTER TABLE wb_bid_actions DROP COLUMN IF EXISTS automation_key;
ALTER TABLE wb_bid_actions DROP COLUMN IF EXISTS automation_observation_key;
