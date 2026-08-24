package tax

import (
	"testing"
	"time"
)

func TestHarvest_LossCandidate(t *testing.T) {
	// Lot acquired at $50k/BTC, current price $40k → unrealized loss
	acquired := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 1_000_000, PriceUSD: 500.0}, // $500 for 0.01 BTC @ $50k
	}

	hr, err := Harvest(lots, nil, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hr.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(hr.Candidates))
	}

	c := hr.Candidates[0]
	if c.LotID != 1 {
		t.Errorf("expected lot ID 1, got %d", c.LotID)
	}
	if c.RemainSat != 1_000_000 {
		t.Errorf("expected 1_000_000 remain sat, got %d", c.RemainSat)
	}

	// cost = $500, market = 0.01 BTC * $40k = $400 → loss = -$100
	wantLoss := -100.0
	if c.UnrealizedLoss < wantLoss-0.01 || c.UnrealizedLoss > wantLoss+0.01 {
		t.Errorf("expected unrealized loss ~%.2f, got %.2f", wantLoss, c.UnrealizedLoss)
	}

	if hr.TotalUnrealizedLoss >= 0 {
		t.Errorf("expected negative TotalUnrealizedLoss, got %.2f", hr.TotalUnrealizedLoss)
	}
}

func TestHarvest_GainNotIncluded(t *testing.T) {
	// Lot acquired at $30k/BTC, current price $50k → unrealized gain → not a candidate
	acquired := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 1_000_000, PriceUSD: 300.0}, // $300 for 0.01 BTC @ $30k
	}

	hr, err := Harvest(lots, nil, FIFO, 50_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hr.Candidates) != 0 {
		t.Errorf("expected 0 candidates for a gain lot, got %d", len(hr.Candidates))
	}
	if hr.TotalUnrealizedLoss != 0 {
		t.Errorf("expected 0 total loss, got %.2f", hr.TotalUnrealizedLoss)
	}
}

func TestHarvest_PartiallyConsumedLot(t *testing.T) {
	// Lot of 2M sats, 1M already disposed, 1M remaining at a loss
	acquired := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	disposed := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 2_000_000, PriceUSD: 1000.0}, // $1k for 0.02 BTC @ $50k
	}
	disposals := []Disposal{
		{DisposedAt: disposed, AmountSat: 1_000_000, ProceedsUSD: 450.0}, // sold half at $45k
	}

	hr, err := Harvest(lots, disposals, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hr.Candidates) != 1 {
		t.Fatalf("expected 1 candidate for remaining half, got %d", len(hr.Candidates))
	}

	c := hr.Candidates[0]
	if c.RemainSat != 1_000_000 {
		t.Errorf("expected 1_000_000 remaining sats, got %d", c.RemainSat)
	}

	// cost of remaining 1M sats = $1000/2 = $500, market = 0.01 BTC * $40k = $400 → loss = -$100
	wantLoss := -100.0
	if c.UnrealizedLoss < wantLoss-0.01 || c.UnrealizedLoss > wantLoss+0.01 {
		t.Errorf("expected unrealized loss ~%.2f, got %.2f", wantLoss, c.UnrealizedLoss)
	}
}

func TestHarvest_YTDRealizedGainLoss(t *testing.T) {
	acquired := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 2_000_000, PriceUSD: 600.0}, // $600 for 0.02 BTC @ $30k
	}
	disposals := []Disposal{
		// Prior year disposal — should NOT count toward YTD
		{DisposedAt: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), AmountSat: 500_000, ProceedsUSD: 250.0},
		// Current year disposal — should count
		{DisposedAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), AmountSat: 500_000, ProceedsUSD: 400.0},
	}

	hr, err := Harvest(lots, disposals, FIFO, 30_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cost per sat = $600/2_000_000 = $0.0003
	// 2025 disposal: 500_000 sats, cost = 0.0003 * 500_000 = $150, proceeds = $400, gain = $250
	wantYTD := 250.0
	if hr.YTDRealizedGainLoss < wantYTD-0.01 || hr.YTDRealizedGainLoss > wantYTD+0.01 {
		t.Errorf("expected YTD realized %.2f, got %.2f", wantYTD, hr.YTDRealizedGainLoss)
	}
}

func TestHarvest_WashSaleRisk(t *testing.T) {
	asOf := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		// Old lot at a loss
		{ID: 1, AcquiredAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), AmountSat: 1_000_000, PriceUSD: 600.0},
		// Recent buy within 30-day wash-sale window
		{ID: 2, AcquiredAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), AmountSat: 500_000, PriceUSD: 250.0},
	}

	hr, err := Harvest(lots, nil, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hr.WashSaleWarning {
		t.Error("expected WashSaleWarning to be true when a recent buy exists")
	}

	// Both lots may be at a loss; all candidates should have WashSaleRisk set
	for _, c := range hr.Candidates {
		if !c.WashSaleRisk {
			t.Errorf("expected WashSaleRisk on candidate lot %d", c.LotID)
		}
	}
}

func TestHarvest_NoWashSaleRiskWhenOldBuy(t *testing.T) {
	asOf := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		// Lot at a loss, acquired >30 days ago — no wash-sale risk
		{ID: 1, AcquiredAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), AmountSat: 1_000_000, PriceUSD: 600.0},
	}

	hr, err := Harvest(lots, nil, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hr.WashSaleWarning {
		t.Error("expected no WashSaleWarning when no recent buys")
	}
}

func TestHarvest_LongTermClassification(t *testing.T) {
	asOf := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		// Held > 1 year → long-term
		{ID: 1, AcquiredAt: time.Date(2023, 7, 1, 0, 0, 0, 0, time.UTC), AmountSat: 1_000_000, PriceUSD: 500.0},
		// Held < 1 year → short-term
		{ID: 2, AcquiredAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), AmountSat: 1_000_000, PriceUSD: 500.0},
	}

	hr, err := Harvest(lots, nil, FIFO, 30_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hr.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(hr.Candidates))
	}

	for _, c := range hr.Candidates {
		switch c.LotID {
		case 1:
			if !c.IsLongTerm {
				t.Error("expected lot 1 (>1yr) to be long-term")
			}
		case 2:
			if c.IsLongTerm {
				t.Error("expected lot 2 (<1yr) to be short-term")
			}
		}
	}
}

func TestHarvest_EstimatedSavings(t *testing.T) {
	acquired := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 100_000_000, PriceUSD: 50_000.0}, // 1 BTC @ $50k
	}

	hr, err := Harvest(lots, nil, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// loss = $40k - $50k = -$10k
	if hr.TotalUnrealizedLoss > -9_999.99 || hr.TotalUnrealizedLoss < -10_000.01 {
		t.Errorf("expected total loss ~-$10,000, got %.2f", hr.TotalUnrealizedLoss)
	}

	wantShort := 10_000.0 * 0.37
	if hr.EstimatedSavingsShortTerm < wantShort-0.01 || hr.EstimatedSavingsShortTerm > wantShort+0.01 {
		t.Errorf("expected short-term savings ~%.2f, got %.2f", wantShort, hr.EstimatedSavingsShortTerm)
	}

	wantLong := 10_000.0 * 0.20
	if hr.EstimatedSavingsLongTerm < wantLong-0.01 || hr.EstimatedSavingsLongTerm > wantLong+0.01 {
		t.Errorf("expected long-term savings ~%.2f, got %.2f", wantLong, hr.EstimatedSavingsLongTerm)
	}
}

func TestHarvest_NetAfterHarvest(t *testing.T) {
	acquired := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	lots := []Lot{
		{ID: 1, AcquiredAt: acquired, AmountSat: 200_000_000, PriceUSD: 100_000.0}, // 2 BTC @ $50k
	}
	// Sold 1 BTC at $55k in YTD for +$5k realized gain
	disposals := []Disposal{
		{DisposedAt: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), AmountSat: 100_000_000, ProceedsUSD: 55_000.0},
	}

	hr, err := Harvest(lots, disposals, FIFO, 40_000.0, asOf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// YTD: sold 1 BTC at $55k, cost = $50k → gain $5k
	if hr.YTDRealizedGainLoss < 4_999.99 || hr.YTDRealizedGainLoss > 5_000.01 {
		t.Errorf("expected YTD realized ~$5000, got %.2f", hr.YTDRealizedGainLoss)
	}

	// Remaining 1 BTC: cost $50k, market $40k → loss -$10k
	// Net = $5k + (-$10k) = -$5k
	wantNet := -5_000.0
	if hr.NetAfterHarvest < wantNet-0.01 || hr.NetAfterHarvest > wantNet+0.01 {
		t.Errorf("expected net after harvest ~%.2f, got %.2f", wantNet, hr.NetAfterHarvest)
	}
}

func TestHarvest_EmptyLotsAndDisposals(t *testing.T) {
	hr, err := Harvest(nil, nil, FIFO, 50_000.0, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hr == nil {
		t.Fatal("expected non-nil result")
	}
	if len(hr.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(hr.Candidates))
	}
	if hr.TotalUnrealizedLoss != 0 {
		t.Errorf("expected 0 loss, got %.2f", hr.TotalUnrealizedLoss)
	}
}

func TestHarvest_InvalidMethod(t *testing.T) {
	_, err := Harvest(nil, nil, Method("invalid"), 50_000.0, time.Now())
	if err == nil {
		t.Error("expected error for invalid method")
	}
}
