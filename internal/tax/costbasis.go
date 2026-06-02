package tax

import (
	"fmt"
	"sort"
	"time"
)

// Method represents a cost basis accounting method.
type Method string

const (
	FIFO Method = "fifo"
	LIFO Method = "lifo"
)

// Lot represents a BTC acquisition lot.
type Lot struct {
	ID          int64
	AcquiredAt  time.Time
	AmountSat   int64   // original amount
	PriceUSD    float64 // total USD cost for this lot
	Source      string
	ExternalID  string
	RemainSat   int64 // remaining unmatched sats (computed during matching)
}

// CostPerSat returns the per-satoshi cost for this lot.
func (l *Lot) CostPerSat() float64 {
	if l.AmountSat == 0 {
		return 0
	}
	return l.PriceUSD / float64(l.AmountSat)
}

// Disposal represents a BTC disposal event (sale, send, spend).
type Disposal struct {
	DisposedAt  time.Time
	AmountSat   int64
	ProceedsUSD float64 // USD received (0 for sends/gifts)
	TxType      string  // "sale", "send", "spend"
	Source      string
	ExternalID  string
}

// TaxableEvent represents a matched disposal-to-lot pair.
type TaxableEvent struct {
	DisposedAt    time.Time
	AcquiredAt    time.Time
	AmountSat     int64
	CostBasisUSD  float64
	ProceedsUSD   float64
	GainLossUSD   float64
	IsLongTerm    bool // held > 1 year
	LotID         int64
	DisposalSource string
	DisposalExtID  string
}

// MatchResult holds the output of matching disposals to lots.
type MatchResult struct {
	Events        []TaxableEvent
	UnmatchedSat  int64 // disposal sats that couldn't be matched to lots
}

// Match pairs disposals against lots using the specified accounting method.
// Lots and disposals are processed in chronological order. For FIFO, the earliest
// lots are consumed first; for LIFO, the most recent lots (acquired before the
// disposal date) are consumed first.
func Match(lots []Lot, disposals []Disposal, method Method) (*MatchResult, error) {
	if method != FIFO && method != LIFO {
		return nil, fmt.Errorf("unsupported method: %s", method)
	}

	// Sort lots by acquisition date ascending.
	sortedLots := make([]Lot, len(lots))
	copy(sortedLots, lots)
	sort.Slice(sortedLots, func(i, j int) bool {
		return sortedLots[i].AcquiredAt.Before(sortedLots[j].AcquiredAt)
	})

	// Initialize remaining sats.
	for i := range sortedLots {
		sortedLots[i].RemainSat = sortedLots[i].AmountSat
	}

	// Sort disposals chronologically.
	sortedDisposals := make([]Disposal, len(disposals))
	copy(sortedDisposals, disposals)
	sort.Slice(sortedDisposals, func(i, j int) bool {
		return sortedDisposals[i].DisposedAt.Before(sortedDisposals[j].DisposedAt)
	})

	result := &MatchResult{}

	for _, d := range sortedDisposals {
		remaining := d.AmountSat
		proceedsPerSat := float64(0)
		if d.AmountSat > 0 {
			proceedsPerSat = d.ProceedsUSD / float64(d.AmountSat)
		}

		// Build candidate lots (acquired before or on disposal date).
		candidates := candidateLots(sortedLots, d.DisposedAt, method)

		for _, idx := range candidates {
			if remaining <= 0 {
				break
			}
			lot := &sortedLots[idx]
			if lot.RemainSat <= 0 {
				continue
			}

			consumed := lot.RemainSat
			if consumed > remaining {
				consumed = remaining
			}

			costBasis := lot.CostPerSat() * float64(consumed)
			proceeds := proceedsPerSat * float64(consumed)

			event := TaxableEvent{
				DisposedAt:     d.DisposedAt,
				AcquiredAt:     lot.AcquiredAt,
				AmountSat:      consumed,
				CostBasisUSD:   costBasis,
				ProceedsUSD:    proceeds,
				GainLossUSD:    proceeds - costBasis,
				IsLongTerm:     d.DisposedAt.Sub(lot.AcquiredAt) > 365*24*time.Hour,
				LotID:          lot.ID,
				DisposalSource: d.Source,
				DisposalExtID:  d.ExternalID,
			}

			result.Events = append(result.Events, event)
			lot.RemainSat -= consumed
			remaining -= consumed
		}

		if remaining > 0 {
			result.UnmatchedSat += remaining
		}
	}

	return result, nil
}

// candidateLots returns indices into sortedLots for lots acquired on or before
// the disposal date, ordered by the accounting method.
func candidateLots(sortedLots []Lot, disposedAt time.Time, method Method) []int {
	var indices []int
	for i, lot := range sortedLots {
		if !lot.AcquiredAt.After(disposedAt) && lot.RemainSat > 0 {
			indices = append(indices, i)
		}
	}

	if method == LIFO {
		// Reverse: most recent first.
		for i, j := 0, len(indices)-1; i < j; i, j = i+1, j-1 {
			indices[i], indices[j] = indices[j], indices[i]
		}
	}
	// FIFO: indices are already in ascending (earliest first) order.
	return indices
}
