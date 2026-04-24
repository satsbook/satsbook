package exchange

import (
	"math"
	"strings"
	"testing"
)

func coinbasePreamble(csvData string) string {
	return "\nTransactions\nUser,Test User,test-uuid\n" + csvData
}

const coinbaseHeader = "ID,Timestamp,Transaction Type,Asset,Quantity Transacted,Price Currency,Price at Transaction,Subtotal,Total (inclusive of fees and/or spread),Fees and/or Spread,Notes"

func TestParseCoinbaseCSV_BTCBuy(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"abc123,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought 0.001 BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "buy" {
		t.Errorf("expected type 'buy', got %q", r.Type)
	}
	if r.AmountBTC != 0.001 {
		t.Errorf("expected AmountBTC 0.001, got %f", r.AmountBTC)
	}
	if r.AmountBTC <= 0 {
		t.Error("expected positive AmountBTC for buy")
	}
	if math.Abs(r.CostBasisUSD-30.24) > 0.001 {
		t.Errorf("expected CostBasisUSD 30.24 (abs of Total), got %f", r.CostBasisUSD)
	}
	if math.Abs(r.FeeUSD-1.00) > 0.001 {
		t.Errorf("expected FeeUSD 1.00, got %f", r.FeeUSD)
	}
}

func TestParseCoinbaseCSV_BTCSend(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"def456,2023-08-01 19:50:07 UTC,Send,BTC,-0.006132,USD,$29244.04,-$179.32,-$179.32,$0.00,Sent BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
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
	if r.AmountBTC >= 0 {
		t.Errorf("expected negative AmountBTC for send, got %f", r.AmountBTC)
	}
}

func TestParseCoinbaseCSV_BTCReceive(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"ghi789,2023-08-01 19:14:09 UTC,Receive,BTC,0.00054535,USD,$29215.145,$15.93,$15.93,$0.00,Received BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
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
	if r.AmountBTC != 0.00054535 {
		t.Errorf("expected AmountBTC 0.00054535, got %f", r.AmountBTC)
	}
	if r.AmountBTC <= 0 {
		t.Error("expected positive AmountBTC for receive")
	}
}

func TestParseCoinbaseCSV_FilterNonBTC(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"eth1,2023-08-01 19:50:07 UTC,Buy,ETH,0.5,USD,$1800.00,$900.00,$910.00,$10.00,Bought ETH\n" +
		"usdc1,2023-08-01 19:50:07 UTC,Sell,USDC,-100,USD,$1.00,-$100.00,-$100.50,$0.50,Sold USDC\n" +
		"btc1,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 BTC row, got %d", len(result.Rows))
	}
	if result.Skipped != 2 {
		t.Errorf("expected 2 skipped non-BTC rows, got %d", result.Skipped)
	}
	if result.Rows[0].Type != "buy" {
		t.Errorf("expected type 'buy', got %q", result.Rows[0].Type)
	}
}

func TestParseCoinbaseCSV_ConvertFromBTC(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"conv1,2023-03-01 22:05:37 UTC,Convert,BTC,-0.00332439,USD,$23541.785,$74.32,$76.27,$1.96,Converted BTC to MATIC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if r.Type != "sale" {
		t.Errorf("expected type 'sale' for convert with negative BTC, got %q", r.Type)
	}
	if r.AmountBTC >= 0 {
		t.Errorf("expected negative AmountBTC for convert-from-BTC, got %f", r.AmountBTC)
	}
}

func TestParseCoinbaseCSV_SkipPreamble(t *testing.T) {
	input := "\nTransactions\nUser,Test User,test-uuid\n" +
		coinbaseHeader + "\n" +
		"abc123,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought 0.001 BTC\n"

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after preamble, got %d", len(result.Rows))
	}
	if result.Rows[0].Type != "buy" {
		t.Errorf("expected type 'buy', got %q", result.Rows[0].Type)
	}
}

func TestParseCoinbaseCSV_EmptyFile(t *testing.T) {
	_, err := ParseCoinbaseCSV(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestParseCoinbaseCSV_HeaderOnly(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows for header-only input, got %d", len(result.Rows))
	}
}

func TestParseCoinbaseCSV_DollarSignParsing(t *testing.T) {
	// Verify $ prefixed amounts parse correctly on a Buy (which uses Total and Fees)
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"abc1,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	r := result.Rows[0]
	if math.Abs(r.CostBasisUSD-30.24) > 0.001 {
		t.Errorf("expected CostBasisUSD 30.24 (parsed from $30.24), got %f", r.CostBasisUSD)
	}
	if math.Abs(r.FeeUSD-1.00) > 0.001 {
		t.Errorf("expected FeeUSD 1.00 (parsed from $1.00), got %f", r.FeeUSD)
	}
}

func TestParseCoinbaseCSV_AdvancedTradeBuy(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"atb1,2023-08-01 19:50:07 UTC,Advanced Trade Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Advanced trade\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0].Type != "buy" {
		t.Errorf("expected type 'buy' for Advanced Trade Buy, got %q", result.Rows[0].Type)
	}
}

func TestParseCoinbaseCSV_AdvancedTradeSell(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"ats1,2023-08-01 19:50:07 UTC,Advanced Trade Sell,BTC,-0.001,USD,$29244.04,-$29.24,-$30.24,$1.00,Advanced trade sell\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0].Type != "sale" {
		t.Errorf("expected type 'sale' for Advanced Trade Sell, got %q", result.Rows[0].Type)
	}
}

func TestParseCoinbaseCSV_RawLinePopulated(t *testing.T) {
	dataLine := "abc123,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought 0.001 BTC"
	input := coinbasePreamble(coinbaseHeader + "\n" + dataLine + "\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0].RawLine == "" {
		t.Error("expected RawLine to be populated, got empty string")
	}
}

func TestParseCoinbaseCSV_BTCSell(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"sell1,2023-06-14 10:57:37 UTC,Sell,BTC,-0.001,USD,$25955.565,$25.96,$25.46,$0.50,Sold BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
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
	if r.AmountBTC >= 0 {
		t.Errorf("expected negative AmountBTC for sell, got %f", r.AmountBTC)
	}
	if math.Abs(r.AmountUSD-25.96) > 0.001 {
		t.Errorf("expected AmountUSD 25.96 (abs of Subtotal), got %f", r.AmountUSD)
	}
	if math.Abs(r.FeeUSD-0.50) > 0.001 {
		t.Errorf("expected FeeUSD 0.50, got %f", r.FeeUSD)
	}
}

func TestCoinbaseRow_IsPurchase(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"buy", true},
		{"sale", false},
		{"send", false},
		{"receive", false},
	}

	for _, tt := range tests {
		r := CoinbaseRow{Type: tt.typ}
		if got := r.IsPurchase(); got != tt.want {
			t.Errorf("IsPurchase() for type %q = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestCoinbaseBalanceMath(t *testing.T) {
	// Realistic mix: 2 buys, 1 receive, 1 send, 1 sell, 1 convert-from-BTC
	// Net sats = buys + receives - sends - sells - converts
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"buy1,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought BTC\n" +
		"buy2,2023-08-15 10:00:00 UTC,Buy,BTC,0.002,USD,$29000.00,$58.00,$59.50,$1.50,Bought BTC\n" +
		"recv1,2023-08-20 12:00:00 UTC,Receive,BTC,0.00054535,USD,$29215.145,$15.93,$15.93,$0.00,Received\n" +
		"send1,2023-09-01 14:00:00 UTC,Send,BTC,-0.006132,USD,$29244.04,-$179.32,-$179.32,$0.00,Sent BTC\n" +
		"sell1,2023-09-15 16:00:00 UTC,Sell,BTC,-0.001,USD,$25955.565,$25.96,$25.46,$0.50,Sold BTC\n" +
		"conv1,2023-10-01 22:05:37 UTC,Convert,BTC,-0.00332439,USD,$23541.785,$74.32,$76.27,$1.96,Converted\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
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

	// 100000 + 200000 + 54535 + (-613200) + (-100000) + (-332439) = -690104 [sic]
	// Actually: let's verify by BTC amounts * 1e8
	// 0.001 = 100000, 0.002 = 200000, 0.00054535 = 54535,
	// -0.006132 = -613200, -0.001 = -100000, -0.00332439 = -332439
	// Total = 100000 + 200000 + 54535 - 613200 - 100000 - 332439 = -691104
	expected := int64(100000 + 200000 + 54535 - 613200 - 100000 - 332439)
	if totalSat != expected {
		t.Errorf("net balance = %d sats, want %d", totalSat, expected)
	}
}

func TestCoinbaseCostBasisMath(t *testing.T) {
	input := coinbasePreamble(coinbaseHeader + "\n" +
		"buy1,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought BTC\n" +
		"buy2,2023-08-15 10:00:00 UTC,Advanced Trade Buy,BTC,0.002,USD,$29000.00,$58.00,$59.50,$1.50,Bought BTC\n" +
		"send1,2023-09-01 14:00:00 UTC,Send,BTC,-0.006132,USD,$29244.04,-$179.32,-$179.32,$0.00,Sent BTC\n")

	result, err := ParseCoinbaseCSV(strings.NewReader(input))
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

	// Buy1: cost basis = 30.24 (Total), fee = 1.00
	// Buy2: cost basis = 59.50 (Total), fee = 1.50
	if math.Abs(totalCostBasis-89.74) > 0.001 {
		t.Errorf("total cost basis = %f, want 89.74", totalCostBasis)
	}
	if math.Abs(totalFeeUSD-2.50) > 0.001 {
		t.Errorf("total fees = %f, want 2.50", totalFeeUSD)
	}
	if totalPurchaseSat != 300000 {
		t.Errorf("total purchase sats = %d, want 300000", totalPurchaseSat)
	}
}
