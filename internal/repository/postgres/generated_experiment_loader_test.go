package postgres

import "testing"

func TestGeneratedExperimentEvidenceLoaderRequiresDatabaseAndCapitalState(t *testing.T) {
	if loader, err := NewGeneratedExperimentEvidenceLoader(nil, nil); err == nil || loader != nil {
		t.Fatalf("NewGeneratedExperimentEvidenceLoader() = %+v/%v", loader, err)
	}
}
