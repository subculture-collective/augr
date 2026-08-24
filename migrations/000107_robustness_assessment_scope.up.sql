LOCK TABLE statistical_robustness_assessments,paper_evaluation_scopes IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE statistical_robustness_assessments
  ADD COLUMN scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT;

CREATE INDEX idx_robustness_assessments_scope ON statistical_robustness_assessments(scope_id,created_at,id) WHERE scope_id IS NOT NULL;

CREATE FUNCTION validate_robustness_assessment_scope() RETURNS TRIGGER AS $$
DECLARE target UUID;
BEGIN
  target:=COALESCE((to_jsonb(NEW)->>'id')::UUID,(to_jsonb(NEW)->>'assessment_id')::UUID);
  PERFORM 1 FROM statistical_robustness_assessments a
    LEFT JOIN paper_evaluation_scopes scope ON scope.id=a.scope_id
    WHERE a.id=target AND (a.scope_id IS NULL OR
      (EXISTS(SELECT 1 FROM robustness_assessment_scenarios scenario WHERE scenario.assessment_id=a.id) AND
       NOT EXISTS(SELECT 1 FROM robustness_assessment_scenarios scenario
         JOIN trade_portfolio_evaluations report ON report.id=scenario.report_id
         WHERE scenario.assessment_id=a.id AND report.account_id<>scope.account_id)));
  IF NOT FOUND THEN RAISE EXCEPTION 'robustness assessment scope does not match report accounts'; END IF;
  RETURN NULL;
END; $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_robustness_assessment_scope
  AFTER INSERT ON statistical_robustness_assessments DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_robustness_assessment_scope();
CREATE CONSTRAINT TRIGGER trg_robustness_scenario_scope
  AFTER INSERT ON robustness_assessment_scenarios DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_robustness_assessment_scope();
