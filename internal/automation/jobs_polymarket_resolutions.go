package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var polymarketResolutionsSpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 * * * *"}

func (o *JobOrchestrator) registerPolymarketResolutionsJob() {
	if o.deps.PolymarketAccountRepo == nil || o.deps.PolymarketResolvedRepo == nil {
		return
	}
	o.Register("polymarket_resolutions", "Process resolved Polymarket markets and update win rates", polymarketResolutionsSpec, o.polymarketResolutions)
}

func (o *JobOrchestrator) polymarketResolutions(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://gamma-api.polymarket.com/markets?closed=true&limit=500&offset=0", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var markets []struct {
		Slug          string          `json:"slug"`
		OutcomePrices json.RawMessage `json:"outcomePrices"`
		EndDate       string          `json:"endDate"`
		Closed        bool            `json:"closed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&markets); err != nil {
		return err
	}
	var processedMarkets, updatedAccounts int
	for _, m := range markets {
		if !m.Closed {
			continue
		}
		var outcomePrices []string
		if len(m.OutcomePrices) > 0 && m.OutcomePrices[0] == '"' {
			if err := json.Unmarshal(m.OutcomePrices, &outcomePrices); err != nil {
				continue
			}
		} else if err := json.Unmarshal(m.OutcomePrices, &outcomePrices); err != nil {
			continue
		}
		if len(outcomePrices) != 2 {
			continue
		}
		winningSide := ""
		switch {
		case outcomePrices[0] == "1":
			winningSide = "YES"
		case outcomePrices[1] == "1":
			winningSide = "NO"
		default:
			continue
		}
		processed, err := o.deps.PolymarketResolvedRepo.IsProcessed(ctx, m.Slug)
		if err != nil || processed {
			continue
		}
		trades, err := o.deps.PolymarketAccountRepo.ListAllTradesBySlug(ctx, m.Slug, 10000)
		if err != nil {
			continue
		}
		counts := map[string]struct{ won, lost int }{}
		for _, t := range trades {
			if strings.EqualFold(t.Action, "buy") {
				if strings.EqualFold(t.Side, winningSide) {
					c := counts[t.AccountAddress]
					c.won++
					counts[t.AccountAddress] = c
				} else {
					c := counts[t.AccountAddress]
					c.lost++
					counts[t.AccountAddress] = c
				}
			}
		}
		for addr, c := range counts {
			if c.won+c.lost == 0 {
				continue
			}
			if err := o.deps.PolymarketAccountRepo.IncrementAccountResolutionStats(ctx, addr, c.won, c.lost); err == nil {
				updatedAccounts++
			}
		}
		if parsed, err := time.Parse(time.RFC3339, m.EndDate); err == nil {
			_ = o.deps.PolymarketResolvedRepo.MarkProcessed(ctx, m.Slug, winningSide, parsed)
		}
		processedMarkets++
	}
	if _, err := o.deps.PolymarketAccountRepo.MarkTracked(ctx, 0.70, 20); err == nil {
	}
	o.logger.Info("polymarket_resolutions: done", slog.Int("markets", processedMarkets), slog.Int("accounts", updatedAccounts))
	return nil
}
