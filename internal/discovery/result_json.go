package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

const discoveryResultSchemaVersion = 2

type jsonFloat struct {
	value    *float64
	sentinel string
}

func splitJSONFloat(value float64) jsonFloat {
	switch {
	case math.IsNaN(value):
		return jsonFloat{sentinel: "nan"}
	case math.IsInf(value, 1):
		return jsonFloat{sentinel: "+inf"}
	case math.IsInf(value, -1):
		return jsonFloat{sentinel: "-inf"}
	default:
		return jsonFloat{value: &value}
	}
}

func joinJSONFloat(raw json.RawMessage, sentinel, field string) (float64, error) {
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

func (e CandidateEvidence) MarshalJSON() ([]byte, error) {
	type plain CandidateEvidence
	closeValue, advValue, atrValue := splitJSONFloat(e.Close), splitJSONFloat(e.ADV), splitJSONFloat(e.ATR)
	return json.Marshal(struct {
		plain
		Close          *float64 `json:"close"`
		CloseNonFinite string   `json:"close_non_finite,omitempty"`
		ADV            *float64 `json:"adv"`
		ADVNonFinite   string   `json:"adv_non_finite,omitempty"`
		ATR            *float64 `json:"atr"`
		ATRNonFinite   string   `json:"atr_non_finite,omitempty"`
	}{plain(e), closeValue.value, closeValue.sentinel, advValue.value, advValue.sentinel, atrValue.value, atrValue.sentinel})
}

func (e *CandidateEvidence) UnmarshalJSON(data []byte) error {
	type plain CandidateEvidence
	var dto struct {
		*plain
		Close          json.RawMessage `json:"close"`
		CloseNonFinite string          `json:"close_non_finite"`
		ADV            json.RawMessage `json:"adv"`
		ADVNonFinite   string          `json:"adv_non_finite"`
		ATR            json.RawMessage `json:"atr"`
		ATRNonFinite   string          `json:"atr_non_finite"`
	}
	dto.plain = (*plain)(e)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	var err error
	if e.Close, err = joinJSONFloat(dto.Close, dto.CloseNonFinite, "close"); err != nil {
		return err
	}
	if e.ADV, err = joinJSONFloat(dto.ADV, dto.ADVNonFinite, "adv"); err != nil {
		return err
	}
	e.ATR, err = joinJSONFloat(dto.ATR, dto.ATRNonFinite, "atr")
	return err
}

func (e SweepEvidence) MarshalJSON() ([]byte, error) {
	type plain SweepEvidence
	value := splitJSONFloat(e.Score)
	return json.Marshal(struct {
		plain
		Score          *float64 `json:"score"`
		ScoreNonFinite string   `json:"score_non_finite,omitempty"`
	}{plain(e), value.value, value.sentinel})
}

func (e *SweepEvidence) UnmarshalJSON(data []byte) error {
	type plain SweepEvidence
	var dto struct {
		*plain
		Score          json.RawMessage `json:"score"`
		ScoreNonFinite string          `json:"score_non_finite"`
	}
	dto.plain = (*plain)(e)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	value, err := joinJSONFloat(dto.Score, dto.ScoreNonFinite, "score")
	e.Score = value
	return err
}

func (e ValidationEvidence) MarshalJSON() ([]byte, error) {
	type plain ValidationEvidence
	value := splitJSONFloat(e.OOSRatio)
	return json.Marshal(struct {
		plain
		OOSRatio          *float64 `json:"oos_ratio"`
		OOSRatioNonFinite string   `json:"oos_ratio_non_finite,omitempty"`
	}{plain(e), value.value, value.sentinel})
}

func (e *ValidationEvidence) UnmarshalJSON(data []byte) error {
	type plain ValidationEvidence
	var dto struct {
		*plain
		OOSRatio          json.RawMessage `json:"oos_ratio"`
		OOSRatioNonFinite string          `json:"oos_ratio_non_finite"`
	}
	dto.plain = (*plain)(e)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	value, err := joinJSONFloat(dto.OOSRatio, dto.OOSRatioNonFinite, "oos_ratio")
	e.OOSRatio = value
	return err
}

func (e DeployedStrategy) MarshalJSON() ([]byte, error) {
	type plain DeployedStrategy
	value := splitJSONFloat(e.Score)
	return json.Marshal(struct {
		plain
		Score          *float64 `json:"score"`
		ScoreNonFinite string   `json:"score_non_finite,omitempty"`
	}{plain(e), value.value, value.sentinel})
}

func (e *DeployedStrategy) UnmarshalJSON(data []byte) error {
	type plain DeployedStrategy
	var dto struct {
		*plain
		Score          json.RawMessage `json:"score"`
		ScoreNonFinite string          `json:"score_non_finite"`
	}
	dto.plain = (*plain)(e)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	value, err := joinJSONFloat(dto.Score, dto.ScoreNonFinite, "score")
	e.Score = value
	return err
}

func (e GenerationAttemptEvidence) MarshalJSON() ([]byte, error) {
	type plain GenerationAttemptEvidence
	value := splitJSONFloat(e.CostUSD)
	return json.Marshal(struct {
		plain
		CostUSD          *float64 `json:"cost_usd"`
		CostUSDNonFinite string   `json:"cost_usd_non_finite,omitempty"`
	}{plain(e), value.value, value.sentinel})
}

func (e *GenerationAttemptEvidence) UnmarshalJSON(data []byte) error {
	type plain GenerationAttemptEvidence
	var dto struct {
		*plain
		CostUSD          json.RawMessage `json:"cost_usd"`
		CostUSDNonFinite string          `json:"cost_usd_non_finite"`
	}
	dto.plain = (*plain)(e)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	value, err := joinJSONFloat(dto.CostUSD, dto.CostUSDNonFinite, "cost_usd")
	e.CostUSD = value
	return err
}

func (r DiscoveryResult) MarshalJSON() ([]byte, error) {
	type plain DiscoveryResult
	return json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		plain
	}{discoveryResultSchemaVersion, plain(r)})
}

func (r *DiscoveryResult) UnmarshalJSON(data []byte) error {
	type plain DiscoveryResult
	var dto struct {
		SchemaVersion int `json:"schema_version"`
		*plain
	}
	dto.plain = (*plain)(r)
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	if dto.SchemaVersion != 0 && dto.SchemaVersion != discoveryResultSchemaVersion {
		return fmt.Errorf("unsupported discovery result schema_version %d", dto.SchemaVersion)
	}
	return nil
}
