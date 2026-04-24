package exchange

import (
	"strings"
	"testing"
)

// --- Trades CSV (CoinTracker Tax) ---

const swanTradesCSV = `Date,Received Quantity,Received Currency,Sent Quantity,Sent Currency,Fee Amount,Fee Currency,Tag
11/16/2024 17:36:30,0.00010975,BTC,10.0000000000000000,USD,0.00,USD,""
07/16/2025 06:06:05,0.00037691,BTC,44.5500000000000000,USD,0.45,USD,""
`

func TestParseSwanTradesCSV_Basic(t *testing.T) {
	result, err := ParseSwanTradesCSV(strings.NewReader(swanTradesCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}

	buy1 := result.Rows[0]
	if buy1.Type != "purchase" {
		t.Errorf("type = %q, want purchase", buy1.Type)
	}
	if buy1.AmountSat != 10975 {
		t.Errorf("sats = %d, want 10975", buy1.AmountSat)
	}
	if buy1.AmountUSD != 10.00 {
		t.Errorf("USD = %f, want 10.00", buy1.AmountUSD)
	}
	if buy1.FeeUSD != 0 {
		t.Errorf("fee = %f, want 0", buy1.FeeUSD)
	}

	buy2 := result.Rows[1]
	if buy2.AmountSat != 37691 {
		t.Errorf("sats = %d, want 37691", buy2.AmountSat)
	}
	if buy2.FeeUSD != 0.45 {
		t.Errorf("fee = %f, want 0.45", buy2.FeeUSD)
	}
	if buy2.CostBasisUSD != 45.00 {
		t.Errorf("cost basis = %f, want 45.00", buy2.CostBasisUSD)
	}
}

func TestParseSwanTradesCSV_BadHeader(t *testing.T) {
	_, err := ParseSwanTradesCSV(strings.NewReader("Wrong,Header\n"))
	if err == nil {
		t.Fatal("expected error for bad header")
	}
}

// --- Transfers CSV (Deposits & Purchases) ---

const swanTransfersCSV = `"Electric Solidus LLC (DBA Swan Bitcoin) 26565 W. Agoura Rd Suite 200 Calabasas, CA 91302"
Phone: 12183797926
Event,Date,Timezone,Status,Transaction ID,Total USD,Transaction USD,Fee USD,Unit Count,Asset Type,BTC Price,Address Label,USD Cost Basis,Acquisition Date
deposit,2024-11-16 17:30:06+00,UTC,settled,,70.00,,,,USD,,,,
purchase,2024-11-16 17:39:46+00,UTC,settled,0a3d6d2d,10.00,10.00,0.00,0.00010975,BTC,91116.17,,,
deposit,2024-12-26 12:06:43+00,UTC,settled,,,,,0.00098195,BTC,,Custodial Transfer from Fortress Trust,,
monthly_fee,2025-08-22 13:23:29+00,UTC,settled,,,,0.00,,USD,,,,
`

func TestParseSwanTransfersCSV_OnlyBTCDeposits(t *testing.T) {
	result, err := ParseSwanTransfersCSV(strings.NewReader(swanTransfersCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only return the BTC deposit — USD deposits, purchases, and fees are skipped
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row (BTC deposit only), got %d", len(result.Rows))
	}

	dep := result.Rows[0]
	if dep.Type != "deposit" {
		t.Errorf("type = %q, want deposit", dep.Type)
	}
	if dep.AmountSat != 98195 {
		t.Errorf("sats = %d, want 98195", dep.AmountSat)
	}
}

func TestParseSwanTransfersCSV_BadHeader(t *testing.T) {
	csv := `"Electric Solidus LLC"
Phone: 123
Wrong,Header
`
	_, err := ParseSwanTransfersCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for bad header")
	}
}

// --- Withdrawals CSV ---

const swanWithdrawalsCSV = `"Electric Solidus LLC (DBA Swan Bitcoin) 26565 W. Agoura Rd Suite 200 Calabasas, CA 91302"
Phone: 12183797926
Created At,Timezone,Transaction ID,Executed At,Canceled At,Status,Bitcoin Amount,Automatic,IP Address
2024-11-27 00:30:25+00,UTC,64d3be26d327c4ca,2024-11-27 04:41:25+00,,settled,0.00074465,f,108.189.12.201
`

func TestParseSwanWithdrawalsCSV_Basic(t *testing.T) {
	result, err := ParseSwanWithdrawalsCSV(strings.NewReader(swanWithdrawalsCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	w := result.Rows[0]
	if w.Type != "withdrawal" {
		t.Errorf("type = %q, want withdrawal", w.Type)
	}
	if w.AmountSat != -74465 {
		t.Errorf("sats = %d, want -74465 (negative for withdrawal)", w.AmountSat)
	}
	if w.TransactionID != "64d3be26d327c4ca" {
		t.Errorf("txID = %q", w.TransactionID)
	}
}

func TestParseSwanWithdrawalsCSV_BadHeader(t *testing.T) {
	csv := `"Electric Solidus LLC"
Phone: 123
Wrong,Header
`
	_, err := ParseSwanWithdrawalsCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for bad header")
	}
}

// --- Balance math ---

func TestSwanBalanceMath(t *testing.T) {
	// Verify: purchases + BTC deposits - withdrawals = custody balance
	trades, _ := ParseSwanTradesCSV(strings.NewReader(swanTradesCSV))
	transfers, _ := ParseSwanTransfersCSV(strings.NewReader(swanTransfersCSV))
	withdrawals, _ := ParseSwanWithdrawalsCSV(strings.NewReader(swanWithdrawalsCSV))

	var total int64
	for _, r := range trades.Rows {
		total += r.AmountSat
	}
	for _, r := range transfers.Rows {
		total += r.AmountSat
	}
	for _, r := range withdrawals.Rows {
		total += r.AmountSat // negative
	}

	// 10975 + 37691 + 98195 - 74465 = 72396
	if total != 72396 {
		t.Errorf("net balance = %d, want 72396", total)
	}
}

func TestSwanRow_IsPurchase(t *testing.T) {
	if !(SwanRow{Type: "purchase"}).IsPurchase() {
		t.Error("purchase should be IsPurchase")
	}
	if (SwanRow{Type: "deposit"}).IsPurchase() {
		t.Error("deposit should not be IsPurchase")
	}
}

func TestSwanRow_IsWithdrawal(t *testing.T) {
	if !(SwanRow{Type: "withdrawal"}).IsWithdrawal() {
		t.Error("withdrawal should be IsWithdrawal")
	}
}
