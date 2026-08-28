package postgres

import (
	"time"

	"github.com/google/uuid"
)

var canonicalRepositoryTestAccountID = uuid.MustParse("00000000-0000-4000-8000-000000000064")
var canonicalRepositoryTestTradeDate = time.Date(2026, time.August, 27, 0, 0, 0, 0, time.UTC)
