package dataset

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ComposeBoundMarketDatasets creates one deterministic manifest from complete,
// already immutable source manifests. It does not select a latest manifest,
// fetch data, or weaken the original observation lineage.
func ComposeBoundMarketDatasets(inputs []*BoundMarketDataset, decisionCutoff time.Time) (*BoundMarketDataset, error) {
	if len(inputs) < 2 || !canonicalTimeValue(decisionCutoff) {
		return nil, fmt.Errorf("dataset composition requires at least two bound inputs and an explicit UTC microsecond cutoff")
	}
	ordered := append([]*BoundMarketDataset(nil), inputs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Manifest().ID().String() < ordered[j].Manifest().ID().String() })
	partitions := make([]PartitionInput, 0)
	payloads := make([]*MarketPayload, 0)
	seenManifests := make(map[uuid.UUID]struct{}, len(ordered))
	for _, input := range ordered {
		if input == nil || input.Manifest() == nil {
			return nil, fmt.Errorf("dataset composition input is incomplete")
		}
		manifest := input.Manifest()
		if _, duplicate := seenManifests[manifest.ID()]; duplicate {
			return nil, fmt.Errorf("dataset composition manifest %s is duplicated", manifest.ID())
		}
		seenManifests[manifest.ID()] = struct{}{}
		if manifest.DecisionCutoff().After(decisionCutoff) {
			return nil, fmt.Errorf("dataset composition cutoff precedes source manifest %s", manifest.ID())
		}
		for _, partition := range manifest.Partitions() {
			converted, err := partitionForComposition(partition)
			if err != nil {
				return nil, fmt.Errorf("dataset composition source %s: %w", manifest.ID(), err)
			}
			partitions = append(partitions, converted)
		}
		payloads = append(payloads, input.Payloads()...)
	}
	manifest, err := NewManifest(ManifestInput{DecisionCutoff: decisionCutoff, Partitions: partitions})
	if err != nil {
		return nil, fmt.Errorf("compose dataset manifest: %w", err)
	}
	bound, err := NewBoundMarketDataset(manifest, payloads)
	if err != nil {
		return nil, fmt.Errorf("bind composed dataset manifest: %w", err)
	}
	return bound, nil
}

func partitionForComposition(partition Partition) (PartitionInput, error) {
	observations := make([]ObservationInput, len(partition.Observations))
	for index, observation := range partition.Observations {
		instrumentID, err := uuid.Parse(observation.InstrumentID)
		if err != nil {
			return PartitionInput{}, fmt.Errorf("observation instrument is invalid: %w", err)
		}
		effectiveAt, err := parseCompositionTime(observation.EffectiveAt)
		if err != nil {
			return PartitionInput{}, err
		}
		observedAt, err := parseCompositionTime(observation.ObservedAt)
		if err != nil {
			return PartitionInput{}, err
		}
		availableAt, err := parseCompositionTime(observation.AvailableAt)
		if err != nil {
			return PartitionInput{}, err
		}
		var publishedAt *time.Time
		if observation.PublishedAt != "" {
			parsed, err := parseCompositionTime(observation.PublishedAt)
			if err != nil {
				return PartitionInput{}, err
			}
			publishedAt = &parsed
		}
		observations[index] = ObservationInput{
			SourceKey: observation.SourceKey, InstrumentID: instrumentID, EffectiveAt: effectiveAt,
			PublishedAt: publishedAt, ObservedAt: observedAt, AvailableAt: availableAt,
			Revision: observation.Revision, CorrectionOf: observation.CorrectionOf, ContentSHA256: observation.ContentSHA256,
			Bid: cloneString(observation.Bid), Ask: cloneString(observation.Ask), Volume: cloneString(observation.Volume), Depth: cloneString(observation.Depth),
		}
	}
	return PartitionInput{
		Kind: partition.Kind, Provider: partition.Provider, Source: partition.Source, Namespace: partition.Namespace,
		RequestSHA256: partition.RequestSHA256, MediaType: partition.MediaType, SymbologyVersion: partition.SymbologyVersion,
		AdjustmentPolicy: partition.AdjustmentPolicy, Timezone: partition.Timezone, Calendar: partition.Calendar,
		Revision: partition.Revision, SupersedesContentSHA256: partition.SupersedesContentSHA256,
		License: partition.License, RetentionPolicy: partition.RetentionPolicy, Observations: observations,
	}, nil
}

func parseCompositionTime(value string) (time.Time, error) {
	parsed, err := time.Parse(canonicalLayout, value)
	if err != nil || !canonicalTimeValue(parsed) {
		return time.Time{}, fmt.Errorf("dataset composition observation time is invalid")
	}
	return parsed, nil
}
