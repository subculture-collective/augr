// Package optionsstrategy compiles reviewed two-leg option rules into immutable
// strategy-catalog identities. It does not evaluate, promote, schedule, or trade.
package optionsstrategy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const (
	CompilerKindV1     = "defined-risk-options-rules"
	CompilerVersionV1  = "defined-risk-options-rules-compiler-v1"
	ConfigSchemaV1     = "defined-risk-options-rules-config-v1"
	DecisionContractV1 = "defined-risk-options-decision-v1"
)

var canonicalUnderlyingPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.]{0,14}$`)

type compiledConfig struct {
	Schema       string                   `json:"schema"`
	OptionsRules rules.OptionsRulesConfig `json:"options_rules"`
}

// Compile returns deterministic family and version identities for an eligible
// vertical. Source identities must identify the exact executable build.
func Compile(config rules.OptionsRulesConfig, sourceCommit, sourceTreeSHA256 string) (*strategycatalog.Family, *strategycatalog.Version, error) {
	if err := rules.ValidateDefinedRiskVertical(&config); err != nil {
		return nil, nil, fmt.Errorf("options strategy compiler: %w", err)
	}
	if !canonicalUnderlyingPattern.MatchString(config.Underlying) {
		return nil, nil, fmt.Errorf("options strategy compiler: underlying must be canonical uppercase symbology")
	}
	typeSlug := strings.ReplaceAll(string(config.StrategyType), "_", "-")
	family, err := strategycatalog.NewFamily(strategycatalog.FamilyInput{
		Slug:         "generated-options-" + strings.ToLower(strings.ReplaceAll(config.Underlying, ".", "-")) + "-" + typeSlug,
		Name:         "Generated " + config.Underlying + " " + typeSlug,
		Thesis:       "Immutable observed-market evaluation of a generated two-leg defined-risk " + typeSlug + " on " + config.Underlying + ".",
		AssetClasses: []instrument.AssetClass{instrument.AssetClassOption},
	})
	if err != nil {
		return nil, nil, err
	}
	configBytes, err := json.Marshal(compiledConfig{Schema: ConfigSchemaV1, OptionsRules: config})
	if err != nil {
		return nil, nil, fmt.Errorf("options strategy compiler: encode rules: %w", err)
	}
	var canonicalObject map[string]any
	if err := json.Unmarshal(configBytes, &canonicalObject); err != nil {
		return nil, nil, fmt.Errorf("options strategy compiler: canonicalize rules: %w", err)
	}
	configBytes, err = json.Marshal(canonicalObject)
	if err != nil {
		return nil, nil, fmt.Errorf("options strategy compiler: canonicalize rules: %w", err)
	}
	version, err := strategycatalog.NewVersion(strategycatalog.VersionInput{
		FamilyID: family.ID(), CompilerKind: CompilerKindV1, CompilerVersion: CompilerVersionV1,
		SourceCommit: sourceCommit, SourceTreeSHA256: sourceTreeSHA256, ConfigSchema: ConfigSchemaV1,
		Config: configBytes, DecisionContract: DecisionContractV1,
		RequiredDatasetKinds: []dataset.Kind{dataset.KindBars, dataset.KindOptionChains, dataset.KindOptionContracts, dataset.KindQuotes},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("options strategy compiler: compile version: %w", err)
	}
	return family, version, nil
}

// Decode reconstructs and validates executable rules from an exact catalog
// version. It rejects other compilers and any changed family binding.
func Decode(family *strategycatalog.Family, version *strategycatalog.Version) (*rules.OptionsRulesConfig, error) {
	if family == nil || version == nil || version.FamilyID() != family.ID() || version.CompilerKind() != CompilerKindV1 ||
		version.CompilerVersion() != CompilerVersionV1 || version.ConfigSchema() != ConfigSchemaV1 || version.DecisionContract() != DecisionContractV1 {
		return nil, fmt.Errorf("options strategy compiler: version identity is not executable")
	}
	var envelope compiledConfig
	if err := json.Unmarshal(version.Config(), &envelope); err != nil || envelope.Schema != ConfigSchemaV1 {
		return nil, fmt.Errorf("options strategy compiler: configuration does not reconstruct")
	}
	if err := rules.ValidateDefinedRiskVertical(&envelope.OptionsRules); err != nil {
		return nil, fmt.Errorf("options strategy compiler: reconstructed rules are invalid: %w", err)
	}
	rebuiltFamily, rebuiltVersion, err := Compile(envelope.OptionsRules, version.SourceCommit(), version.SourceTreeSHA256())
	if err != nil || rebuiltFamily.ID() != family.ID() || rebuiltFamily.Digest() != family.Digest() || rebuiltVersion.ID() != version.ID() || rebuiltVersion.Digest() != version.Digest() {
		return nil, fmt.Errorf("options strategy compiler: family and version do not reconstruct")
	}
	result := envelope.OptionsRules
	return &result, nil
}
