LOCK TABLE statistical_robustness_assessments IN ACCESS EXCLUSIVE MODE;

DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM statistical_robustness_assessments WHERE scope_id IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot roll back migration 107 while scoped robustness assessments exist';
  END IF;
END $$;

DROP TRIGGER trg_robustness_scenario_scope ON robustness_assessment_scenarios;
DROP TRIGGER trg_robustness_assessment_scope ON statistical_robustness_assessments;
DROP FUNCTION validate_robustness_assessment_scope();
DROP INDEX idx_robustness_assessments_scope;
ALTER TABLE statistical_robustness_assessments DROP COLUMN scope_id;
