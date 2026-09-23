package universe

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

// PreMarketConfig holds scoring parameters for the pre-market screener.
type PreMarketConfig struct {
	MinADV           float64 // default 500000
	MinPrice         float64 // default 5.0
	MaxPrice         float64 // default 500.0
	TopN             int     // default 30
	VolumeWeight     float64 // default 0.4
	MomentumWeight   float64 // default 0.3
	VolatilityWeight float64 // default 0.3
	// MaxSkippedFraction is the share of requested tickers that may be
	// skipped (stale, duplicate, unexpected or missing snapshots) before the
	// whole screen fails. Zero means the default 0.25.
	MaxSkippedFraction float64
	now                func() time.Time
}

const defaultMaxSkippedFraction = 0.25

// ScoredTicker is the result of the pre-market screener for a single ticker.
type ScoredTicker struct {
	Ticker    string   `json:"ticker"`
	Score     float64  `json:"score"`
	Reasons   []string `json:"reasons"`
	DayVolume float64  `json:"day_volume"`
	DayClose  float64  `json:"day_close"`
	ChangePct float64  `json:"change_pct"`
	GapPct    float64  `json:"gap_pct"`
}

// DefaultPreMarketConfig returns sensible defaults for the screener.
func DefaultPreMarketConfig() PreMarketConfig {
	return PreMarketConfig{
		MinADV:           500_000,
		MinPrice:         5.0,
		MaxPrice:         500.0,
		TopN:             30,
		VolumeWeight:     0.4,
		MomentumWeight:   0.3,
		VolatilityWeight: 0.3,

		MaxSkippedFraction: defaultMaxSkippedFraction,
		now:                time.Now,
	}
}

// RunPreMarketScreen fetches bulk snapshot from Polygon, scores each ticker,
// updates watch_score in universe, and returns the top N.
func RunPreMarketScreen(
	ctx context.Context,
	polygonClient *polygon.Client,
	repo UniverseRepository,
	cfg PreMarketConfig,
	logger *slog.Logger,
) ([]ScoredTicker, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// 1. Get every active ticker from repo.
	tickers, err := listAllActiveUniverseTickers(ctx, repo)
	if err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("screener: active universe is empty")
	}

	// 2. Extract ticker symbols and batch them for the snapshot API.
	// The free Polygon tier doesn't allow fetching all tickers at once (403),
	// and URLs have length limits. Batch into chunks of ~100 tickers.
	const batchSize = 100
	symbols := make([]string, 0, len(tickers))
	for _, t := range tickers {
		symbols = append(symbols, t.Ticker)
	}

	var snapshots []polygon.TickerSnapshot
	now := time.Now()
	if cfg.now != nil {
		now = cfg.now()
	}
	// Individual bad snapshots are skipped and counted; the screen fails only
	// when too many tickers were unusable.
	skipped := map[string]int{}
	skippedTotal := 0
	skip := func(kind, symbol string, attrs ...any) {
		skipped[kind]++
		if kind != "unexpected" {
			// Unexpected symbols were never requested, so they do not count
			// against the requested universe.
			skippedTotal++
		}
		logger.Warn("screener: skipping ticker snapshot", append([]any{slog.String("reason", kind), slog.String("ticker", symbol)}, attrs...)...)
	}
	for i := 0; i < len(symbols); i += batchSize {
		end := i + batchSize
		if end > len(symbols) {
			end = len(symbols)
		}
		batch, err := polygonClient.BulkSnapshot(ctx, symbols[i:end])
		if err != nil {
			return nil, fmt.Errorf("screener: snapshot batch %d failed after %d complete snapshots: %w", i/batchSize+1, len(snapshots), err)
		}
		requested := make(map[string]struct{}, len(symbols[i:end]))
		for _, symbol := range symbols[i:end] {
			requested[strings.ToUpper(strings.TrimSpace(symbol))] = struct{}{}
		}
		seen := make(map[string]struct{}, len(batch))
		received := make(map[string]struct{}, len(batch))
		for _, snapshot := range batch {
			symbol := strings.ToUpper(strings.TrimSpace(snapshot.Ticker))
			if _, ok := requested[symbol]; !ok {
				skip("unexpected", symbol)
				continue
			}
			received[symbol] = struct{}{}
			if _, duplicate := seen[symbol]; duplicate {
				skip("duplicate", symbol)
				continue
			}
			if !currentEasternSnapshot(now, snapshot.UpdatedAt()) {
				skip("stale", symbol, slog.Time("updated_at", snapshot.UpdatedAt()))
				continue
			}
			seen[symbol] = struct{}{}
			snapshots = append(snapshots, snapshot)
		}
		for symbol := range requested {
			if _, ok := received[symbol]; !ok {
				skip("missing", symbol)
			}
		}

		// Rate limit pause between batches.
		if end < len(symbols) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	}

	maxSkipped := cfg.MaxSkippedFraction
	if maxSkipped <= 0 {
		maxSkipped = defaultMaxSkippedFraction
	}
	if skippedTotal > 0 {
		fraction := float64(skippedTotal) / float64(len(symbols))
		if fraction > maxSkipped {
			return nil, fmt.Errorf("screener: %d of %d tickers skipped (%.0f%% > %.0f%% allowed): stale=%d duplicate=%d unexpected=%d missing=%d",
				skippedTotal, len(symbols), fraction*100, maxSkipped*100, skipped["stale"], skipped["duplicate"], skipped["unexpected"], skipped["missing"])
		}
		logger.Warn("screener: some snapshots skipped",
			slog.Int("skipped", skippedTotal),
			slog.Int("requested", len(symbols)),
			slog.Int("stale", skipped["stale"]),
			slog.Int("duplicate", skipped["duplicate"]),
			slog.Int("unexpected", skipped["unexpected"]),
			slog.Int("missing", skipped["missing"]),
		)
	}
	logger.Info("screener: received snapshots", slog.Int("count", len(snapshots)), slog.Int("skipped", skippedTotal))

	// 4. Score each snapshot.
	scored := make([]ScoredTicker, 0, len(snapshots))
	for _, snap := range snapshots {
		dayVolume := snap.Day.Volume
		dayClose := snap.Day.Close
		dayOpen := snap.Day.Open
		prevClose := snap.PrevDay.Close
		prevVolume := snap.PrevDay.Volume
		changePct := snap.TodaysChangePct

		// Skip if below ADV, price out of range, or missing data.
		if dayVolume < cfg.MinADV {
			continue
		}
		if dayClose < cfg.MinPrice || dayClose > cfg.MaxPrice {
			continue
		}

		// GapPct: (DayOpen - PrevClose) / PrevClose * 100
		var gapPct float64
		if prevClose > 0 {
			gapPct = (dayOpen - prevClose) / prevClose * 100
		}

		// VolumeRatio: DayVolume / PrevVolume (handle zero)
		var volumeRatio float64
		if prevVolume > 0 {
			volumeRatio = dayVolume / prevVolume
		}

		// Score components.
		volScore := math.Min(volumeRatio/3, 1)
		momScore := math.Min(math.Abs(gapPct)/5, 1)
		volatScore := math.Min(math.Abs(changePct)/3, 1)

		score := cfg.VolumeWeight*volScore + cfg.MomentumWeight*momScore + cfg.VolatilityWeight*volatScore

		// Build reasons.
		var reasons []string
		if volumeRatio >= 1.5 {
			reasons = append(reasons, fmt.Sprintf("Volume surge %.1fx", volumeRatio))
		}
		if gapPct > 0.5 {
			reasons = append(reasons, fmt.Sprintf("Gap up %.1f%%", gapPct))
		} else if gapPct < -0.5 {
			reasons = append(reasons, fmt.Sprintf("Gap down %.1f%%", gapPct))
		}
		if changePct > 1.0 {
			reasons = append(reasons, fmt.Sprintf("Up %.1f%% today", changePct))
		} else if changePct < -1.0 {
			reasons = append(reasons, fmt.Sprintf("Down %.1f%% today", changePct))
		}

		scored = append(scored, ScoredTicker{
			Ticker:    snap.Ticker,
			Score:     score,
			Reasons:   reasons,
			DayVolume: dayVolume,
			DayClose:  dayClose,
			ChangePct: changePct,
			GapPct:    gapPct,
		})
	}

	// 5. Sort by score descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// 6. Update repo watch_score for each scored ticker.
	scoreErrors := 0
	for _, s := range scored {
		if err := repo.UpdateScore(ctx, s.Ticker, s.Score); err != nil {
			scoreErrors++
			logger.Warn("screener: failed to update score",
				slog.String("ticker", s.Ticker),
				slog.Any("error", err),
			)
		}
	}
	if scoreErrors > 0 {
		return nil, fmt.Errorf("screener: failed to persist %d of %d scores", scoreErrors, len(scored))
	}

	// 7. Return top N.
	if cfg.TopN > 0 && len(scored) > cfg.TopN {
		scored = scored[:cfg.TopN]
	}

	logger.Info("screener: complete", slog.Int("scored", len(scored)))
	return scored, nil
}

func listAllActiveUniverseTickers(ctx context.Context, repo UniverseRepository) ([]TrackedTicker, error) {
	active := true
	const pageSize = 1000
	var tickers []TrackedTicker
	for offset := 0; ; {
		page, err := repo.List(ctx, ListFilter{Active: &active}, pageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("screener: list active tickers at offset %d: %w", offset, err)
		}
		tickers = append(tickers, page...)
		if len(page) < pageSize {
			break
		}
		offset += len(page)
	}

	unique := make([]TrackedTicker, 0, len(tickers))
	seen := make(map[string]struct{}, len(tickers))
	for _, ticker := range tickers {
		symbol := strings.ToUpper(strings.TrimSpace(ticker.Ticker))
		if symbol == "" {
			continue
		}
		if _, exists := seen[symbol]; exists {
			continue
		}
		seen[symbol] = struct{}{}
		ticker.Ticker = symbol
		unique = append(unique, ticker)
	}
	return unique, nil
}

func currentEasternSnapshot(now, updated time.Time) bool {
	if updated.IsZero() {
		return false
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return false
	}
	nowET := now.In(loc)
	updatedET := updated.In(loc)
	nowYear, nowMonth, nowDay := nowET.Date()
	updatedYear, updatedMonth, updatedDay := updatedET.Date()
	if nowYear != updatedYear || nowMonth != updatedMonth || nowDay != updatedDay {
		return false
	}
	sessionStart := time.Date(nowYear, nowMonth, nowDay, 4, 0, 0, 0, loc)
	return !updatedET.Before(sessionStart) && !updatedET.After(nowET.Add(5*time.Minute))
}
