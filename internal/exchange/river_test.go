package exchange

import (
	"strings"
	"testing"
)

func TestParseRiverCSV_ValidBuys(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy
2024-10-03 20:00:02,4.00,USD,0.00006536,BTC,,,Buy`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}

	// First row: buy with fee
	r := result.Rows[0]
	if r.Type != "buy" {
		t.Errorf("expected type 'buy', got %q", r.Type)
	}
	if r.AmountBTC != 0.00006093 {
		t.Errorf("expected AmountBTC 0.00006093, got %f", r.AmountBTC)
	}
	if r.AmountSat != 6093 {
		t.Errorf("expected AmountSat 6093, got %d", r.AmountSat)
	}
	if r.AmountUSD != 3.96 {
		t.Errorf("expected AmountUSD 3.96, got %f", r.AmountUSD)
	}
	if r.CostBasisUSD != 3.96 {
		t.Errorf("expected CostBasisUSD 3.96, got %f", r.CostBasisUSD)
	}
	if r.FeeUSD != 0.04 {
		t.Errorf("expected FeeUSD 0.04, got %f", r.FeeUSD)
	}
	if !r.IsPurchase() {
		t.Error("expected IsPurchase() to be true")
	}

	// Second row: buy with no fee
	r2 := result.Rows[1]
	if r2.FeeUSD != 0 {
		t.Errorf("expected FeeUSD 0, got %f", r2.FeeUSD)
	}
	if r2.AmountBTC != 0.00006536 {
		t.Errorf("expected AmountBTC 0.00006536, got %f", r2.AmountBTC)
	}
}

func TestParseRiverCSV_Withdrawal(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2026-03-16 11:24:47,0.01336638,BTC,,,,,Withdrawal`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "send" {
		t.Errorf("expected type 'send', got %q", r.Type)
	}
	if r.AmountBTC != -0.01336638 {
		t.Errorf("expected AmountBTC -0.01336638, got %f", r.AmountBTC)
	}
	if r.AmountSat != -1336638 {
		t.Errorf("expected AmountSat -1336638, got %d", r.AmountSat)
	}
	if r.IsPurchase() {
		t.Error("expected IsPurchase() to be false")
	}
}

func TestParseRiverCSV_EmptyTagBTCSend(t *testing.T) {
	// Empty tag with BTC sent = on-chain send
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-11-27 00:36:42,0.00482484,BTC,,,0.00000242,BTC,`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "send" {
		t.Errorf("expected type 'send', got %q", r.Type)
	}
	if r.AmountBTC != -0.00482484 {
		t.Errorf("expected AmountBTC -0.00482484, got %f", r.AmountBTC)
	}
	if r.FeeBTC != 0.00000242 {
		t.Errorf("expected FeeBTC 0.00000242, got %f", r.FeeBTC)
	}
}

func TestParseRiverCSV_MixedTransactions(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy
2024-11-27 00:36:42,0.00482484,BTC,,,0.00000242,BTC,
2024-11-27 00:37:44,41.58,USD,0.00045038,BTC,0.42,USD,Buy
2026-03-16 11:24:47,0.01336638,BTC,,,,,Withdrawal`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(result.Errors), result.Errors)
	}

	// Verify types
	types := []string{"buy", "send", "buy", "send"}
	for i, expected := range types {
		if result.Rows[i].Type != expected {
			t.Errorf("row %d: expected type %q, got %q", i, expected, result.Rows[i].Type)
		}
	}
}

func TestParseRiverCSV_InvalidHeader(t *testing.T) {
	csv := `Wrong,Header,Format
1,2,3`

	_, err := ParseRiverCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for invalid header")
	}
}

func TestParseRiverCSV_EmptyFile(t *testing.T) {
	_, err := ParseRiverCSV(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestParseRiverCSV_HeaderOnly(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestParseRiverCSV_MalformedRow(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
not-a-date,3.96,USD,0.00006093,BTC,0.04,USD,Buy
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 valid row, got %d", len(result.Rows))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestParseRiverCSV_RawLinePopulated(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Rows[0].RawLine == "" {
		t.Error("expected RawLine to be populated")
	}
}

func TestParseRiverCSV_TooFewColumns(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26,3.96,USD`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestParseRiverCSV_SellTransaction(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,0.001,BTC,65.00,USD,0.50,USD,Sell`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "sale" {
		t.Errorf("expected type 'sale', got %q", r.Type)
	}
	if r.AmountBTC != -0.001 {
		t.Errorf("expected AmountBTC -0.001, got %f", r.AmountBTC)
	}
	if r.AmountUSD != 65.00 {
		t.Errorf("expected AmountUSD 65.00, got %f", r.AmountUSD)
	}
	if r.FeeUSD != 0.50 {
		t.Errorf("expected FeeUSD 0.50, got %f", r.FeeUSD)
	}
}

func TestParseRiverCSV_ReceiveTransaction(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,,,0.005,BTC,,,Receive`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "receive" {
		t.Errorf("expected type 'receive', got %q", r.Type)
	}
	if r.AmountBTC != 0.005 {
		t.Errorf("expected AmountBTC 0.005, got %f", r.AmountBTC)
	}
	if r.AmountSat != 500000 {
		t.Errorf("expected AmountSat 500000, got %d", r.AmountSat)
	}
}

func TestRiverBalanceMath(t *testing.T) {
	// Realistic mix: 2 buys, 1 receive, 1 send, 1 withdrawal, 1 sell
	// Net sats = buys + receives - sends - withdrawals - sells
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy
2024-10-03 20:00:02,4.00,USD,0.00006536,BTC,,,Buy
2024-11-01 10:00:00,,,0.00500000,BTC,,,Receive
2024-11-27 00:36:42,0.00482484,BTC,,,0.00000242,BTC,
2024-12-01 12:00:00,0.001,BTC,65.00,USD,0.50,USD,Sell
2026-03-16 11:24:47,0.01336638,BTC,,,,,Withdrawal`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 6 {
		t.Fatalf("expected 6 rows, got %d (errors: %v)", len(result.Rows), result.Errors)
	}

	var totalSat int64
	for _, r := range result.Rows {
		totalSat += r.AmountSat
	}

	// 6093 + 6536 + 500000 + (-482484) + (-100000) + (-1336638) = -1406493
	if totalSat != -1406493 {
		t.Errorf("net balance = %d sats, want -1406493", totalSat)
	}
}

func TestRiverCostBasisMath(t *testing.T) {
	csv := `Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy
2024-10-03 20:00:02,4.00,USD,0.00006536,BTC,,,Buy
2024-11-27 00:36:42,0.00482484,BTC,,,0.00000242,BTC,`

	result, err := ParseRiverCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var totalCostBasis float64
	var totalFeeUSD float64
	var totalPurchaseSat int64
	for _, r := range result.Rows {
		if r.IsPurchase() {
			totalCostBasis += r.CostBasisUSD
			totalFeeUSD += r.FeeUSD
			totalPurchaseSat += r.AmountSat
		}
	}

	if totalCostBasis != 7.96 {
		t.Errorf("total cost basis = %f, want 7.96", totalCostBasis)
	}
	if totalFeeUSD != 0.04 {
		t.Errorf("total fees = %f, want 0.04", totalFeeUSD)
	}
	if totalPurchaseSat != 12629 {
		t.Errorf("total purchase sats = %d, want 12629", totalPurchaseSat)
	}
}
