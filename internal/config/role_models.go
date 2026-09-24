package config

import (
	"fmt"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ParseRoleModels parses LLM_ROLE_MODELS: comma-separated role=model pairs
// such as "risk_manager=openai/gpt-6-astra,invest_judge=openai/gpt-6-sol".
// Roles must be defined agent roles and may appear once.
func ParseRoleModels(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		role, model, ok := strings.Cut(entry, "=")
		role, model = strings.TrimSpace(role), strings.TrimSpace(model)
		if !ok || role == "" || model == "" {
			return nil, fmt.Errorf("entry %q must be role=model", entry)
		}
		if !domain.AgentRole(role).IsValid() {
			return nil, fmt.Errorf("unknown agent role %q", role)
		}
		if _, dup := out[role]; dup {
			return nil, fmt.Errorf("agent role %q is listed more than once", role)
		}
		out[role] = model
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// parseRoleModelsLenient is used by Load; Validate reports the parse error.
func parseRoleModelsLenient(raw string) map[string]string {
	parsed, err := ParseRoleModels(raw)
	if err != nil {
		return nil
	}
	return parsed
}
