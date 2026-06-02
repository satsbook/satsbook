package tax

import (
	"math"
	"testing"
	"time"
)

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func assertClose(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s: got %.4f, want %.4f", label, got, want)
	}
}

func TestMatch_FIFO_SingleLotSingleDisposal(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2024, 1, 1), AmountSat: 1_000_000, PriceUSD: 430.00, Source: "strike"},
	}
	disposals := []Disposal{
		{DisposedAt: date(2024, 6, 1), AmountSat: 500_000, ProceedsUSD: 300.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}

	e := result.Events[0]
	if e.AmountSat != 500_000 {
		t.Errorf("amount: got %d", e.AmountSat)
	}
	assertClose(t, "cost basis", e.CostBasisUSD, 215.00) // 500k/1M * 430
	assertClose(t, "proceeds", e.ProceedsUSD, 300.00)
	assertClose(t, "gain", e.GainLossUSD, 85.00)
	if e.IsLongTerm {
		t.Error("should be short-term (held < 1 year)")
	}
	if result.UnmatchedSat != 0 {
		t.Errorf("unmatched: %d", result.UnmatchedSat)
	}
}

func TestMatch_FIFO_MultipleLots(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2023, 1, 1), AmountSat: 500_000, PriceUSD: 100.00, Source: "strike"},
		{ID: 2, AcquiredAt: date(2023, 6, 1), AmountSat: 500_000, PriceUSD: 150.00, Source: "river"},
		{ID: 3, AcquiredAt: date(2024, 1, 1), AmountSat: 500_000, PriceUSD: 200.00, Source: "coinbase"},
	}
	// Sell 750k sats — should consume lot 1 (500k) + lot 2 (250k) under FIFO.
	disposals := []Disposal{
		{DisposedAt: date(2024, 7, 1), AmountSat: 750_000, ProceedsUSD: 500.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result.Events))
	}

	// Event 1: lot 1 fully consumed (500k sats)
	e1 := result.Events[0]
	if e1.LotID != 1 || e1.AmountSat != 500_000 {
		t.Errorf("event 1: lot=%d, amount=%d", e1.LotID, e1.AmountSat)
	}
	assertClose(t, "e1 cost", e1.CostBasisUSD, 100.00)
	assertClose(t, "e1 proceeds", e1.ProceedsUSD, 500.0*500_000/750_000) // 333.33
	if !e1.IsLongTerm {
		t.Error("e1 should be long-term (2023-01 to 2024-07)")
	}

	// Event 2: lot 2 partially consumed (250k sats)
	e2 := result.Events[1]
	if e2.LotID != 2 || e2.AmountSat != 250_000 {
		t.Errorf("event 2: lot=%d, amount=%d", e2.LotID, e2.AmountSat)
	}
	assertClose(t, "e2 cost", e2.CostBasisUSD, 75.00) // 250k/500k * 150
	if !e2.IsLongTerm {
		t.Error("e2 should be long-term (2023-06 to 2024-07)")
	}

	if result.UnmatchedSat != 0 {
		t.Errorf("unmatched: %d", result.UnmatchedSat)
	}
}

func TestMatch_LIFO_MultipleLots(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2023, 1, 1), AmountSat: 500_000, PriceUSD: 100.00},
		{ID: 2, AcquiredAt: date(2023, 6, 1), AmountSat: 500_000, PriceUSD: 150.00},
		{ID: 3, AcquiredAt: date(2024, 1, 1), AmountSat: 500_000, PriceUSD: 200.00},
	}
	// Sell 600k sats — LIFO should consume lot 3 (500k) + lot 2 (100k).
	disposals := []Disposal{
		{DisposedAt: date(2024, 7, 1), AmountSat: 600_000, ProceedsUSD: 400.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, LIFO)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result.Events))
	}

	// LIFO: lot 3 consumed first
	e1 := result.Events[0]
	if e1.LotID != 3 || e1.AmountSat != 500_000 {
		t.Errorf("event 1: lot=%d, amount=%d", e1.LotID, e1.AmountSat)
	}
	assertClose(t, "e1 cost", e1.CostBasisUSD, 200.00)
	if e1.IsLongTerm {
		t.Error("e1 should be short-term (2024-01 to 2024-07)")
	}

	// Then lot 2 partially
	e2 := result.Events[1]
	if e2.LotID != 2 || e2.AmountSat != 100_000 {
		t.Errorf("event 2: lot=%d, amount=%d", e2.LotID, e2.AmountSat)
	}
	assertClose(t, "e2 cost", e2.CostBasisUSD, 30.00) // 100k/500k * 150
}

func TestMatch_MultipleDisposals(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2023, 1, 1), AmountSat: 1_000_000, PriceUSD: 300.00},
	}
	disposals := []Disposal{
		{DisposedAt: date(2024, 3, 1), AmountSat: 400_000, ProceedsUSD: 200.00, TxType: "sale"},
		{DisposedAt: date(2024, 6, 1), AmountSat: 300_000, ProceedsUSD: 180.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result.Events))
	}

	// Disposal 1: 400k sats from lot 1
	assertClose(t, "d1 cost", result.Events[0].CostBasisUSD, 120.00) // 400k/1M * 300
	assertClose(t, "d1 gain", result.Events[0].GainLossUSD, 80.00)

	// Disposal 2: 300k sats from lot 1 (remaining 600k)
	assertClose(t, "d2 cost", result.Events[1].CostBasisUSD, 90.00) // 300k/1M * 300
	assertClose(t, "d2 gain", result.Events[1].GainLossUSD, 90.00)

	if result.UnmatchedSat != 0 {
		t.Errorf("unmatched: %d", result.UnmatchedSat)
	}
}

func TestMatch_UnmatchedDisposal(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2024, 1, 1), AmountSat: 100_000, PriceUSD: 50.00},
	}
	disposals := []Disposal{
		{DisposedAt: date(2024, 6, 1), AmountSat: 300_000, ProceedsUSD: 200.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].AmountSat != 100_000 {
		t.Errorf("matched amount: %d", result.Events[0].AmountSat)
	}
	if result.UnmatchedSat != 200_000 {
		t.Errorf("unmatched: got %d, want 200000", result.UnmatchedSat)
	}
}

func TestMatch_NoDisposals(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2024, 1, 1), AmountSat: 1_000_000, PriceUSD: 500.00},
	}

	result, err := Match(lots, nil, FIFO)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(result.Events))
	}
}

func TestMatch_NoLots(t *testing.T) {
	disposals := []Disposal{
		{DisposedAt: date(2024, 6, 1), AmountSat: 100_000, ProceedsUSD: 50.00, TxType: "sale"},
	}

	result, err := Match(nil, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}
	if result.UnmatchedSat != 100_000 {
		t.Errorf("unmatched: %d", result.UnmatchedSat)
	}
}

func TestMatch_SendWithZeroProceeds(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2023, 1, 1), AmountSat: 1_000_000, PriceUSD: 300.00},
	}
	disposals := []Disposal{
		{DisposedAt: date(2024, 3, 1), AmountSat: 500_000, ProceedsUSD: 0, TxType: "send"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}

	e := result.Events[0]
	assertClose(t, "proceeds", e.ProceedsUSD, 0)
	assertClose(t, "cost", e.CostBasisUSD, 150.00) // 500k/1M * 300
	assertClose(t, "loss", e.GainLossUSD, -150.00)
}

func TestMatch_LongTermBoundary(t *testing.T) {
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2023, 1, 1), AmountSat: 100_000, PriceUSD: 50.00},
	}

	// Exactly 365 days later — still short-term (need > 365 days).
	disposals := []Disposal{
		{DisposedAt: date(2024, 1, 1), AmountSat: 50_000, ProceedsUSD: 30.00, TxType: "sale"},
	}
	result, _ := Match(lots, disposals, FIFO)
	if result.Events[0].IsLongTerm {
		t.Error("exactly 365 days should be short-term")
	}

	// 366 days — long-term.
	disposals2 := []Disposal{
		{DisposedAt: date(2024, 1, 2), AmountSat: 50_000, ProceedsUSD: 30.00, TxType: "sale"},
	}
	result2, _ := Match(lots, disposals2, FIFO)
	if !result2.Events[0].IsLongTerm {
		t.Error("366 days should be long-term")
	}
}

func TestMatch_InvalidMethod(t *testing.T) {
	_, err := Match(nil, nil, "hifo")
	if err == nil {
		t.Error("expected error for invalid method")
	}
}

func TestMatch_FutureLotNotConsumed(t *testing.T) {
	// Lot acquired AFTER disposal should not be consumed.
	lots := []Lot{
		{ID: 1, AcquiredAt: date(2024, 6, 1), AmountSat: 1_000_000, PriceUSD: 500.00},
	}
	disposals := []Disposal{
		{DisposedAt: date(2024, 1, 1), AmountSat: 500_000, ProceedsUSD: 250.00, TxType: "sale"},
	}

	result, err := Match(lots, disposals, FIFO)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 0 {
		t.Errorf("future lot should not be matched, got %d events", len(result.Events))
	}
	if result.UnmatchedSat != 500_000 {
		t.Errorf("unmatched: %d", result.UnmatchedSat)
	}
}

func TestSummarize(t *testing.T) {
	mr := &MatchResult{
		Events: []TaxableEvent{
			{AmountSat: 500_000, CostBasisUSD: 100, ProceedsUSD: 200, GainLossUSD: 100, IsLongTerm: true},
			{AmountSat: 300_000, CostBasisUSD: 80, ProceedsUSD: 50, GainLossUSD: -30, IsLongTerm: false},
			{AmountSat: 200_000, CostBasisUSD: 60, ProceedsUSD: 90, GainLossUSD: 30, IsLongTerm: false},
		},
		UnmatchedSat: 10_000,
	}

	s := Summarize(mr)
	assertClose(t, "total proceeds", s.TotalProceeds, 340.00)
	assertClose(t, "total cost", s.TotalCostBasis, 240.00)
	assertClose(t, "total gain", s.TotalGainLoss, 100.00)
	assertClose(t, "long-term gain", s.LongTermGainLoss, 100.00)
	assertClose(t, "short-term gain", s.ShortTermGainLoss, 0.00) // -30 + 30
	if s.LongTermCount != 1 {
		t.Errorf("long-term count: %d", s.LongTermCount)
	}
	if s.ShortTermCount != 2 {
		t.Errorf("short-term count: %d", s.ShortTermCount)
	}
	if s.UnmatchedSat != 10_000 {
		t.Errorf("unmatched: %d", s.UnmatchedSat)
	}
}
