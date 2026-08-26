package automation

import (
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func completedDailyBarFresh(marketType domain.MarketType, now, latest time.Time) bool {
	switch marketType.Normalize() {
	case domain.MarketTypeStock:
		return dailyBarFresh(now, latest)
	case domain.MarketTypeCrypto:
		expected := now.UTC().AddDate(0, 0, -1)
		return sameMarketDate(expected, latest.UTC())
	default:
		return false
	}
}
