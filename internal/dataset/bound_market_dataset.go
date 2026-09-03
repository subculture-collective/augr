package dataset

import (
	"fmt"

	"github.com/google/uuid"
)

// MarketPayloadBinding identifies the single immutable payload consumed by one
// canonical manifest observation.
type MarketPayloadBinding struct {
	PartitionSequence   int
	ObservationSequence int
	PayloadID           uuid.UUID
	ContentSHA256       string
}

// BoundMarketDataset is a complete manifest-to-payload graph. Construction is
// fail closed: every observation must resolve to exactly one typed payload and
// no unreferenced payload may be smuggled into the write.
type BoundMarketDataset struct {
	manifest *Manifest
	payloads []*MarketPayload
	bindings []MarketPayloadBinding
}

func NewBoundMarketDataset(manifest *Manifest, payloads []*MarketPayload) (*BoundMarketDataset, error) {
	if manifest == nil || len(payloads) == 0 {
		return nil, fmt.Errorf("bound market dataset requires a manifest and payloads")
	}
	byDigest := make(map[string]*MarketPayload, len(payloads))
	for _, payload := range payloads {
		if payload == nil {
			return nil, fmt.Errorf("bound market dataset payload is nil")
		}
		if _, exists := byDigest[payload.Digest()]; exists {
			return nil, fmt.Errorf("bound market dataset payload %s is duplicated", payload.Digest())
		}
		byDigest[payload.Digest()] = payload
	}

	used := make(map[string]struct{}, len(payloads))
	bindings := make([]MarketPayloadBinding, 0, len(payloads))
	for _, partition := range manifest.Partitions() {
		for _, observation := range partition.Observations {
			payload, exists := byDigest[observation.ContentSHA256]
			if !exists {
				return nil, fmt.Errorf("manifest observation %d/%d has no immutable payload", partition.Sequence, observation.Sequence)
			}
			if _, exists := used[observation.ContentSHA256]; exists {
				return nil, fmt.Errorf("immutable payload %s is bound more than once", observation.ContentSHA256)
			}
			if err := validateObservationPayloadBinding(partition, observation, payload); err != nil {
				return nil, err
			}
			used[observation.ContentSHA256] = struct{}{}
			bindings = append(bindings, MarketPayloadBinding{
				PartitionSequence: partition.Sequence, ObservationSequence: observation.Sequence,
				PayloadID: payload.ID(), ContentSHA256: payload.Digest(),
			})
		}
	}
	if len(used) != len(payloads) {
		return nil, fmt.Errorf("bound market dataset contains %d unreferenced payloads", len(payloads)-len(used))
	}
	return &BoundMarketDataset{manifest: manifest, payloads: append([]*MarketPayload(nil), payloads...), bindings: bindings}, nil
}

func validateObservationPayloadBinding(partition Partition, observation Observation, payload *MarketPayload) error {
	metadata := payload.Metadata()
	if !partitionKindAcceptsPayload(partition.Kind, metadata.Kind) || partition.Provider != metadata.Provider ||
		partition.AdjustmentPolicy != metadata.AdjustmentPolicy || observation.InstrumentID != metadata.InstrumentID.String() ||
		observation.EffectiveAt != formatTime(metadata.EffectiveAt) || observation.ObservedAt != formatTime(metadata.ObservedAt) ||
		observation.AvailableAt != formatTime(metadata.AvailableAt) || observation.Revision != metadata.Revision {
		return fmt.Errorf("manifest observation %d/%d does not reconstruct payload metadata", partition.Sequence, observation.Sequence)
	}
	publishedAt := ""
	if metadata.PublishedAt != nil {
		publishedAt = formatTime(*metadata.PublishedAt)
	}
	if observation.PublishedAt != publishedAt || observation.CorrectionOf != metadata.CorrectionOfSHA256 {
		return fmt.Errorf("manifest observation %d/%d publication or correction lineage differs from payload", partition.Sequence, observation.Sequence)
	}
	return nil
}

func partitionKindAcceptsPayload(kind Kind, payloadKind MarketPayloadKind) bool {
	switch payloadKind {
	case MarketPayloadStockBar, MarketPayloadOptionBar:
		return kind == KindBars
	case MarketPayloadOptionQuote:
		return kind == KindQuotes
	case MarketPayloadOptionContract:
		return kind == KindOptionContracts
	case MarketPayloadOptionSnapshot:
		return kind == KindOptionChains
	case MarketPayloadOptionTrade:
		return kind == KindExternalObject
	default:
		return false
	}
}

func (value *BoundMarketDataset) Manifest() *Manifest {
	if value == nil {
		return nil
	}
	return value.manifest
}

func (value *BoundMarketDataset) Payloads() []*MarketPayload {
	if value == nil {
		return nil
	}
	return append([]*MarketPayload(nil), value.payloads...)
}

func (value *BoundMarketDataset) Bindings() []MarketPayloadBinding {
	if value == nil {
		return nil
	}
	return append([]MarketPayloadBinding(nil), value.bindings...)
}
