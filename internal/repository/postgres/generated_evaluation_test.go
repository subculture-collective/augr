package postgres

import "testing"

func TestGeneratedEvaluationSourceRequiresSharedDatabaseEvidence(t *testing.T) {
	if source, err := NewGeneratedEvaluationSource(nil, nil); err == nil || source != nil {
		t.Fatalf("NewGeneratedEvaluationSource() = %+v/%v", source, err)
	}
}
