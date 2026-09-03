package generativestrategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

// ProposalEvidence is one server-selected, immutable-scope prompt. It carries
// no account, deployment, scheduling, promotion, or execution authority.
type ProposalEvidence struct {
	Key               string
	Universe          Universe
	AllowedDataFields []AllowedDataField
	ImmutableSummary  string
}

type ProposalEvidenceSource interface {
	ListEligibleGeneratedProposalEvidence(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) ([]ProposalEvidence, error)
}

type ModelProposalSource struct {
	evidence         ProposalEvidenceSource
	family           *strategycatalog.Family
	providerName     string
	provider         llm.Provider
	model            string
	sourceCommit     string
	sourceTreeSHA256 string
}

func NewModelProposalSource(
	evidence ProposalEvidenceSource,
	family *strategycatalog.Family,
	providerName string,
	provider llm.Provider,
	model string,
	sourceCommit string,
	sourceTreeSHA256 string,
) (*ModelProposalSource, error) {
	if evidence == nil || family == nil || provider == nil || !tokenPattern.MatchString(providerName) || strings.TrimSpace(model) == "" ||
		len(sourceCommit) != 40 || !digestPattern.MatchString(sourceTreeSHA256) {
		return nil, fmt.Errorf("model proposal source requires evidence, family, provider, model, and exact source identity")
	}
	return &ModelProposalSource{
		evidence: evidence, family: family, providerName: providerName, provider: provider, model: strings.TrimSpace(model),
		sourceCommit: sourceCommit, sourceTreeSHA256: sourceTreeSHA256,
	}, nil
}

func (source *ModelProposalSource) ListEligibleGeneratedProposals(ctx context.Context, accountID, scopeID uuid.UUID, limit int) ([]EligibleProposal, error) {
	if source == nil || source.evidence == nil || source.family == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return nil, fmt.Errorf("model proposal source requires exact account, scope, and bounded limit")
	}
	evidence, err := source.evidence.ListEligibleGeneratedProposalEvidence(ctx, accountID, scopeID, source.family.ID(), limit)
	if err != nil {
		return nil, fmt.Errorf("list immutable generated proposal evidence: %w", err)
	}
	if len(evidence) > limit {
		return nil, fmt.Errorf("immutable generated proposal evidence exceeded requested limit")
	}
	items := make([]EligibleProposal, 0, len(evidence))
	seen := make(map[string]struct{}, len(evidence))
	for index, item := range evidence {
		if !tokenPattern.MatchString(item.Key) || len(item.Universe.Instruments) == 0 || item.Universe.Benchmark == uuid.Nil ||
			len(item.AllowedDataFields) == 0 || strings.TrimSpace(item.ImmutableSummary) == "" {
			return nil, fmt.Errorf("immutable generated proposal evidence %d is incomplete", index)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			return nil, fmt.Errorf("immutable generated proposal evidence key %q is duplicated", item.Key)
		}
		seen[item.Key] = struct{}{}
		request, err := (ModelProposalGenerator{}).Generate(ctx, ModelProposalRequest{
			Family: source.family, Universe: item.Universe, SpecKey: item.Key, AllowedDataFields: item.AllowedDataFields,
			ProviderName: source.providerName, Provider: source.provider, Model: source.model, ImmutableSummary: item.ImmutableSummary,
			SourceCommit: source.sourceCommit, SourceTreeSHA256: source.sourceTreeSHA256,
		})
		if err != nil {
			return nil, fmt.Errorf("generate typed proposal %q: %w", item.Key, err)
		}
		items = append(items, EligibleProposal{Key: item.Key, Request: request})
	}
	return items, nil
}

// ReviewedDailyStockFamily is the single initial production family for model
// proposals backed by immutable daily equity bars.
func ReviewedDailyStockFamily() (*strategycatalog.Family, error) {
	return strategycatalog.NewFamily(strategycatalog.FamilyInput{
		Slug:         "generated-daily-stock-v1",
		Name:         "Generated daily stock v1",
		Thesis:       "Typed, bounded daily equity hypotheses derived only from a configured immutable evaluation scope.",
		AssetClasses: []instrument.AssetClass{instrument.AssetClassEquity},
	})
}

var _ EligibleProposalSource = (*ModelProposalSource)(nil)
