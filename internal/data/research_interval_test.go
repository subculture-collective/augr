package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type intervalLoaderStub struct {
	manifestSymbolLoaderStub
	interval       ResearchInterval
	err            error
	requestedScope uuid.UUID
}

func (stub *intervalLoaderStub) LoadResearchInterval(_ context.Context, scope uuid.UUID) (ResearchInterval, error) {
	stub.requestedScope = scope
	return stub.interval, stub.err
}

func TestResearchIntervalReconstructsExactScopeAndFailsClosed(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	wantErr := errors.New("scope quarantined")
	for _, tc := range []struct {
		name     string
		interval ResearchInterval
		err      error
		valid    bool
	}{
		{"valid", ResearchInterval{Start: start, End: end}, nil, true},
		{"missing", ResearchInterval{}, nil, false},
		{"zero-width", ResearchInterval{Start: start, End: start}, nil, false},
		{"reversed", ResearchInterval{Start: end, End: start}, nil, false},
		{"nanoseconds", ResearchInterval{Start: start.Add(time.Nanosecond), End: end}, nil, false},
		{"non-UTC", ResearchInterval{Start: start.In(time.FixedZone("offset", 3600)), End: end}, nil, false},
		{"quarantined", ResearchInterval{Start: start, End: end}, wantErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := uuid.New()
			loader := &intervalLoaderStub{interval: tc.interval, err: tc.err}
			service, err := NewManifestBoundDataService(&DataService{}, scope, loader)
			if err != nil {
				t.Fatal(err)
			}
			interval, err := service.ResearchInterval(context.Background())
			if (err == nil) != tc.valid {
				t.Fatalf("interval=%v err=%v", interval, err)
			}
			if !tc.valid && interval != nil {
				t.Fatal("failed scope returned an interval")
			}
			if tc.valid && *interval != tc.interval {
				t.Fatal("scope dates changed")
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("underlying error was not preserved")
			}
			if loader.requestedScope != scope {
				t.Fatal("different scope was reconstructed")
			}
		})
	}
}

func TestResearchIntervalNeverFallsBackForBoundReaders(t *testing.T) {
	service, err := NewManifestBoundDataService(&DataService{}, uuid.New(), &manifestSymbolLoaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	if interval, err := service.ResearchInterval(context.Background()); err == nil || interval != nil {
		t.Fatal("bound reader without interval support fell back to wall clock")
	}
	if interval, err := (&DataService{}).ResearchInterval(context.Background()); err != nil || interval != nil {
		t.Fatal("ordinary live service was changed")
	}
}
