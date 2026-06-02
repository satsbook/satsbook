package tax

import (
	"encoding/csv"
	"fmt"
	"io"
	"time"
)

// Form8949Row represents a single row on IRS Form 8949.
type Form8949Row struct {
	Description    string // e.g. "0.001 BTC"
	DateAcquired   time.Time
	DateSold       time.Time
	Proceeds       float64
	CostBasis      float64
	GainOrLoss     float64
	HoldingPeriod  string // "Short-term" or "Long-term"
}

// EventToForm8949Row converts a TaxableEvent to a Form 8949 row.
func EventToForm8949Row(e TaxableEvent) Form8949Row {
	btc := float64(e.AmountSat) / 1e8
	period := "Short-term"
	if e.IsLongTerm {
		period = "Long-term"
	}
	return Form8949Row{
		Description:   fmt.Sprintf("%.8f BTC", btc),
		DateAcquired:  e.AcquiredAt,
		DateSold:      e.DisposedAt,
		Proceeds:      e.ProceedsUSD,
		CostBasis:     e.CostBasisUSD,
		GainOrLoss:    e.GainLossUSD,
		HoldingPeriod: period,
	}
}

// WriteForm8949CSV writes Form 8949 rows as a TurboTax-compatible CSV.
func WriteForm8949CSV(w io.Writer, events []TaxableEvent) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{
		"Description of Property",
		"Date Acquired",
		"Date Sold",
		"Proceeds",
		"Cost or Other Basis",
		"Gain or Loss",
		"Holding Period",
	}
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	for _, e := range events {
		row := EventToForm8949Row(e)
		record := []string{
			row.Description,
			row.DateAcquired.Format("01/02/2006"),
			row.DateSold.Format("01/02/2006"),
			fmt.Sprintf("%.2f", row.Proceeds),
			fmt.Sprintf("%.2f", row.CostBasis),
			fmt.Sprintf("%.2f", row.GainOrLoss),
			row.HoldingPeriod,
		}
		if err := cw.Write(record); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	return cw.Error()
}

// TaxSummary holds aggregate tax stats for a period.
type TaxSummary struct {
	TotalProceeds       float64
	TotalCostBasis      float64
	TotalGainLoss       float64
	ShortTermGainLoss   float64
	LongTermGainLoss    float64
	ShortTermCount      int
	LongTermCount       int
	TotalDisposals      int
	UnmatchedSat        int64
}

// Summarize computes aggregate tax stats from a MatchResult.
func Summarize(mr *MatchResult) TaxSummary {
	var s TaxSummary
	s.UnmatchedSat = mr.UnmatchedSat
	s.TotalDisposals = len(mr.Events)

	for _, e := range mr.Events {
		s.TotalProceeds += e.ProceedsUSD
		s.TotalCostBasis += e.CostBasisUSD
		s.TotalGainLoss += e.GainLossUSD
		if e.IsLongTerm {
			s.LongTermGainLoss += e.GainLossUSD
			s.LongTermCount++
		} else {
			s.ShortTermGainLoss += e.GainLossUSD
			s.ShortTermCount++
		}
	}

	return s
}
