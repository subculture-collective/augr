package promotion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
)

const (
	ActivationSchemaV1 = "strategy-promotion-activation-v1"
	ActivationAction   = "activate"
	SuspensionAction   = "suspend"
	activationDomain   = "strategy-promotion-activation"
)

var activationSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ActivationInput struct {
	Action               string
	DeploymentID         uuid.UUID
	DeploymentSHA256     string
	DecisionID           uuid.UUID
	DecisionSHA256       string
	StrategyID           uuid.UUID
	SourceVersionID      uuid.UUID
	RuntimeVersionID     uuid.UUID
	RuntimeVersionSHA256 string
	AccountID            uuid.UUID
	ScopeID              uuid.UUID
	CapitalBindingID     uuid.UUID
	ScheduleCron         string
	Timezone             string
	RiskPolicyVersion    string
	PriorActivationID    uuid.UUID
	PriorActivationSHA   string
}

type activationCanonical struct {
	Schema               string `json:"schema"`
	Action               string `json:"action"`
	DeploymentID         string `json:"deployment_id"`
	DeploymentSHA256     string `json:"deployment_sha256"`
	DecisionID           string `json:"decision_id"`
	DecisionSHA256       string `json:"decision_sha256"`
	StrategyID           string `json:"strategy_id"`
	SourceVersionID      string `json:"source_version_id"`
	RuntimeVersionID     string `json:"runtime_version_id"`
	RuntimeVersionSHA256 string `json:"runtime_version_sha256"`
	AccountID            string `json:"account_id"`
	ScopeID              string `json:"scope_id"`
	CapitalBindingID     string `json:"capital_binding_id"`
	ScheduleCron         string `json:"schedule_cron"`
	Timezone             string `json:"timezone"`
	RiskPolicyVersion    string `json:"risk_policy_version"`
	PriorActivationID    string `json:"prior_activation_id"`
	PriorActivationSHA   string `json:"prior_activation_sha256"`
}

// Activation is the immutable receipt connecting an authoritative promotion
// head to the exact runtime strategy version produced by its projection.
type Activation struct {
	canonical activationCanonical
	bytes     json.RawMessage
	digest    string
	id        uuid.UUID
}

func NewActivation(input ActivationInput) (*Activation, error) {
	if input.Action != ActivationAction && input.Action != SuspensionAction {
		return nil, fmt.Errorf("promotion activation action is invalid")
	}
	if input.DeploymentID == uuid.Nil || input.DecisionID == uuid.Nil || input.StrategyID == uuid.Nil ||
		input.SourceVersionID == uuid.Nil || input.RuntimeVersionID == uuid.Nil || input.AccountID == uuid.Nil ||
		input.ScopeID == uuid.Nil || input.CapitalBindingID == uuid.Nil ||
		!activationSHA256Pattern.MatchString(input.DeploymentSHA256) ||
		!activationSHA256Pattern.MatchString(input.DecisionSHA256) ||
		!activationSHA256Pattern.MatchString(input.RuntimeVersionSHA256) ||
		strings.TrimSpace(input.Timezone) == "" || input.Timezone != strings.TrimSpace(input.Timezone) ||
		strings.TrimSpace(input.RiskPolicyVersion) == "" || input.RiskPolicyVersion != strings.TrimSpace(input.RiskPolicyVersion) {
		return nil, fmt.Errorf("promotion activation identity is invalid")
	}
	if input.Action == ActivationAction && strings.TrimSpace(input.ScheduleCron) == "" {
		return nil, fmt.Errorf("promotion activation schedule is required")
	}
	if input.Action == SuspensionAction && input.ScheduleCron != "" {
		return nil, fmt.Errorf("promotion suspension cannot retain a schedule")
	}
	priorID := ""
	if input.PriorActivationID != uuid.Nil {
		priorID = input.PriorActivationID.String()
		if !activationSHA256Pattern.MatchString(input.PriorActivationSHA) {
			return nil, fmt.Errorf("promotion prior activation digest is invalid")
		}
	} else if input.PriorActivationSHA != "" {
		return nil, fmt.Errorf("promotion prior activation identity is incomplete")
	}
	canonical := activationCanonical{
		Schema: ActivationSchemaV1, Action: input.Action, DeploymentID: input.DeploymentID.String(),
		DeploymentSHA256: input.DeploymentSHA256, DecisionID: input.DecisionID.String(), DecisionSHA256: input.DecisionSHA256,
		StrategyID: input.StrategyID.String(), SourceVersionID: input.SourceVersionID.String(), RuntimeVersionID: input.RuntimeVersionID.String(),
		RuntimeVersionSHA256: input.RuntimeVersionSHA256, AccountID: input.AccountID.String(), ScopeID: input.ScopeID.String(),
		CapitalBindingID: input.CapitalBindingID.String(), ScheduleCron: input.ScheduleCron, Timezone: input.Timezone,
		RiskPolicyVersion: input.RiskPolicyVersion, PriorActivationID: priorID, PriorActivationSHA: input.PriorActivationSHA,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	return &Activation{canonical: canonical, bytes: raw, digest: digest,
		id: economicid.DeterministicUUID(activationDomain, ActivationSchemaV1+"@sha256:"+digest)}, nil
}

func ActivationFromCanonical(id uuid.UUID, digest string, raw []byte) (*Activation, error) {
	if id == uuid.Nil || !activationSHA256Pattern.MatchString(digest) {
		return nil, fmt.Errorf("promotion activation envelope is invalid")
	}
	var canonical activationCanonical
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("promotion activation contains trailing data")
	}
	input, err := activationInputFromCanonical(canonical)
	if err != nil {
		return nil, err
	}
	value, err := NewActivation(input)
	if err != nil {
		return nil, err
	}
	if canonical.Schema != ActivationSchemaV1 || value.ID() != id || value.Digest() != digest || !bytes.Equal(value.bytes, raw) {
		return nil, fmt.Errorf("promotion activation canonical identity does not reconstruct")
	}
	return value, nil
}

func activationInputFromCanonical(value activationCanonical) (ActivationInput, error) {
	parse := func(raw string) (uuid.UUID, error) {
		if raw == "" {
			return uuid.Nil, nil
		}
		return uuid.Parse(raw)
	}
	deploymentID, err := parse(value.DeploymentID)
	if err != nil {
		return ActivationInput{}, err
	}
	decisionID, err := parse(value.DecisionID)
	if err != nil {
		return ActivationInput{}, err
	}
	strategyID, err := parse(value.StrategyID)
	if err != nil {
		return ActivationInput{}, err
	}
	sourceVersionID, err := parse(value.SourceVersionID)
	if err != nil {
		return ActivationInput{}, err
	}
	runtimeVersionID, err := parse(value.RuntimeVersionID)
	if err != nil {
		return ActivationInput{}, err
	}
	accountID, err := parse(value.AccountID)
	if err != nil {
		return ActivationInput{}, err
	}
	scopeID, err := parse(value.ScopeID)
	if err != nil {
		return ActivationInput{}, err
	}
	bindingID, err := parse(value.CapitalBindingID)
	if err != nil {
		return ActivationInput{}, err
	}
	priorID, err := parse(value.PriorActivationID)
	if err != nil {
		return ActivationInput{}, err
	}
	return ActivationInput{Action: value.Action, DeploymentID: deploymentID, DeploymentSHA256: value.DeploymentSHA256,
		DecisionID: decisionID, DecisionSHA256: value.DecisionSHA256, StrategyID: strategyID,
		SourceVersionID: sourceVersionID, RuntimeVersionID: runtimeVersionID, RuntimeVersionSHA256: value.RuntimeVersionSHA256,
		AccountID: accountID, ScopeID: scopeID, CapitalBindingID: bindingID, ScheduleCron: value.ScheduleCron,
		Timezone: value.Timezone, RiskPolicyVersion: value.RiskPolicyVersion, PriorActivationID: priorID,
		PriorActivationSHA: value.PriorActivationSHA}, nil
}

func (value *Activation) ID() uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return value.id
}
func (value *Activation) Digest() string {
	if value == nil {
		return ""
	}
	return value.digest
}
func (value *Activation) CanonicalBytes() json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value.bytes...)
}
func (value *Activation) Action() string {
	if value == nil {
		return ""
	}
	return value.canonical.Action
}
func (value *Activation) DecisionID() uuid.UUID {
	id, _ := uuid.Parse(value.canonical.DecisionID)
	return id
}
func (value *Activation) StrategyID() uuid.UUID {
	id, _ := uuid.Parse(value.canonical.StrategyID)
	return id
}
func (value *Activation) RuntimeVersionID() uuid.UUID {
	id, _ := uuid.Parse(value.canonical.RuntimeVersionID)
	return id
}
