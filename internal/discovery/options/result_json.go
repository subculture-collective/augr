package options

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

const optionsDiscoveryResultSchemaVersion = 2

func splitOptionsJSONFloat(value float64) (*float64, string) {
	switch {
	case math.IsNaN(value):
		return nil, "nan"
	case math.IsInf(value, 1):
		return nil, "+inf"
	case math.IsInf(value, -1):
		return nil, "-inf"
	default:
		return &value, ""
	}
}

func joinOptionsJSONFloat(raw json.RawMessage, sentinel, field string) (float64, error) {
	if len(raw) == 0 {
		if sentinel != "" {
			return 0, fmt.Errorf("%s_non_finite requires %s", field, field)
		}
		return 0, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		switch sentinel {
		case "nan":
			return math.NaN(), nil
		case "+inf":
			return math.Inf(1), nil
		case "-inf":
			return math.Inf(-1), nil
		case "":
			return 0, fmt.Errorf("%s is null without %s_non_finite", field, field)
		default:
			return 0, fmt.Errorf("invalid %s_non_finite %q", field, sentinel)
		}
	}
	if sentinel != "" {
		return 0, fmt.Errorf("%s_non_finite conflicts with finite %s", field, field)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("decode %s: %w", field, err)
	}
	return value, nil
}

func (s OptionsDeployedStrategy) MarshalJSON() ([]byte, error) {
	type plain OptionsDeployedStrategy
	value, sentinel := splitOptionsJSONFloat(s.Score)
	return json.Marshal(struct {
		plain
		Score          *float64 `json:"score"`
		ScoreNonFinite string   `json:"score_non_finite,omitempty"`
	}{plain(s), value, sentinel})
}

func (s *OptionsDeployedStrategy) UnmarshalJSON(data []byte) error {
	type plain OptionsDeployedStrategy
	var dto struct {
		*plain
		Score          json.RawMessage `json:"score"`
		ScoreNonFinite string          `json:"score_non_finite"`
	}
	dto.plain = (*plain)(s)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	value, err := joinOptionsJSONFloat(dto.Score, dto.ScoreNonFinite, "score")
	s.Score = value
	return err
}

func (r OptionsDiscoveryResult) MarshalJSON() ([]byte, error) {
	type plain OptionsDiscoveryResult
	return json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		plain
	}{optionsDiscoveryResultSchemaVersion, plain(r)})
}

func (r *OptionsDiscoveryResult) UnmarshalJSON(data []byte) error {
	type plain OptionsDiscoveryResult
	var dto struct {
		SchemaVersion int `json:"schema_version"`
		*plain
	}
	dto.plain = (*plain)(r)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	if dto.SchemaVersion != 0 && dto.SchemaVersion != optionsDiscoveryResultSchemaVersion {
		return fmt.Errorf("unsupported options discovery result schema_version %d", dto.SchemaVersion)
	}
	return nil
}
