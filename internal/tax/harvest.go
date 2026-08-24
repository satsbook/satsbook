package tax

import "time"

// HarvestCandidate is an open lot currently at an unrealized loss vs current BTC price.
type HarvestCandidate struct {
	LotID          int64
	Source         string
	ExternalID     string
	AcquiredAt     time.Time
	RemainSat      int64
	CostBasisUSD   float64 // cost of remaining sats
	MarketValueUSD float64 // market value at current price
	UnrealizedLoss float64 // negative: marketValue - costBasis
	IsLongTerm     bool    // held > 1 year as of asOf
	WashSaleRisk   bool    // another lot acquired within 30 days before asOf
}

// HarvestResult summarizes the tax-loss harvesting opportunity.
type HarvestResult struct {
	Candidates                []HarvestCandidate
	TotalUnrealizedLoss       float64 // sum of unrealized losses (negative)
	EstimatedSavingsShortTerm float64 // total loss * 37% ordinary income rate
	EstimatedSavingsLongTerm  float64 // total loss * 20% LTCG rate
	YTDRealizedGainLoss       float64 // realized G/L in current calendar year
	NetAfterHarvest           float64 // YTDRealizedGainLoss + TotalUnrealizedLoss
	WashSaleWarning           bool    // a recent buy creates wash-sale risk
	DaysUntilYearEnd          int
}

// Harvest identifies open lots at an unrealized loss given the current BTC price and
// computes the YTD realized gain/loss context. asOf is typically time.Now().
func Harvest(lots []Lot, disposals []Disposal, method Method, currentPriceUSD float64, asOf time.Time) (*HarvestResult, error) {
	matchResult, err := Match(lots, disposals, method)
	if err != nil {
		return nil, err
	}

	// Sats consumed per lot from matched disposals.
	consumedPerLot := make(map[int64]int64, len(lots))
	for _, e := range matchResult.Events {
		consumedPerLot[e.LotID] += e.AmountSat
	}

	// YTD realized gain/loss (events with disposal date in current calendar year up to asOf).
	yearStart := time.Date(asOf.Year(), 1, 1, 0, 0, 0, 0, asOf.Location())
	var ytdRealized float64
	for _, e := range matchResult.Events {
		if !e.DisposedAt.Before(yearStart) && !e.DisposedAt.After(asOf) {
			ytdRealized += e.GainLossUSD
		}
	}

	// Wash-sale risk: any lot acquired in the 30 days before asOf means a hypothetical
	// sale today could be disallowed by the wash-sale rule.
	washSaleCutoff := asOf.AddDate(0, 0, -30)
	recentBuy := false
	for _, lot := range lots {
		if !lot.AcquiredAt.Before(washSaleCutoff) && lot.AcquiredAt.Before(asOf) {
			recentBuy = true
			break
		}
	}

	yearEnd := time.Date(asOf.Year(), 12, 31, 23, 59, 59, 0, asOf.Location())
	daysUntilYearEnd := int(yearEnd.Sub(asOf).Hours() / 24)

	hr := &HarvestResult{
		YTDRealizedGainLoss: ytdRealized,
		DaysUntilYearEnd:    daysUntilYearEnd,
	}

	for _, lot := range lots {
		remainSat := lot.AmountSat - consumedPerLot[lot.ID]
		if remainSat <= 0 {
			continue
		}

		costBasis := lot.CostPerSat() * float64(remainSat)
		marketValue := float64(remainSat) / 1e8 * currentPriceUSD
		unrealized := marketValue - costBasis

		if unrealized >= 0 {
			continue
		}

		hr.Candidates = append(hr.Candidates, HarvestCandidate{
			LotID:          lot.ID,
			Source:         lot.Source,
			ExternalID:     lot.ExternalID,
			AcquiredAt:     lot.AcquiredAt,
			RemainSat:      remainSat,
			CostBasisUSD:   costBasis,
			MarketValueUSD: marketValue,
			UnrealizedLoss: unrealized,
			IsLongTerm:     asOf.Sub(lot.AcquiredAt) > 365*24*time.Hour,
			WashSaleRisk:   recentBuy,
		})

		hr.TotalUnrealizedLoss += unrealized
	}

	hr.EstimatedSavingsShortTerm = -hr.TotalUnrealizedLoss * 0.37
	hr.EstimatedSavingsLongTerm = -hr.TotalUnrealizedLoss * 0.20
	hr.NetAfterHarvest = ytdRealized + hr.TotalUnrealizedLoss
	hr.WashSaleWarning = recentBuy && len(hr.Candidates) > 0

	return hr, nil
}
