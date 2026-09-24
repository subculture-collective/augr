package data

import "time"

// CurrentSnapshotWindowSkew bounds how far past a requested window end a
// current-state snapshot may be fetched and still count as "as of" that end.
const CurrentSnapshotWindowSkew = 5 * time.Minute

// CurrentSnapshotAsOf returns the timestamp to record for a snapshot that a
// provider can only measure now (StockTwits streams, Bluesky search). Callers
// fix their window end before other fetches run, so a snapshot taken seconds
// later would fall outside the window and be discarded. When the fetch lands
// within CurrentSnapshotWindowSkew after "to", the snapshot is stamped at
// "to". A historical window keeps the real fetch time, so current sentiment
// is never attributed to the past.
func CurrentSnapshotAsOf(measuredAt, to time.Time) time.Time {
	if to.IsZero() || !measuredAt.After(to) || measuredAt.Sub(to) > CurrentSnapshotWindowSkew {
		return measuredAt
	}
	return to
}
