package exchange

import (
	"strings"
	"testing"
)

const swanTestCSV = `"Electric Solidus LLC (DBA Swan Bitcoin) 26565 W. Agoura Rd Suite 200 Calabasas, CA 91302"
Phone: 12183797926
Event,Date,Timezone,Status,Transaction ID,Total USD,Transaction USD,Fee USD,Unit Count,Asset Type,BTC Price,Address Label,USD Cost Basis,Acquisition Date
deposit,2024-11-16 17:30:06+00,UTC,settled,,70.00,,,,USD,,,,
purchase,2024-11-16 17:39:46+00,UTC,settled,0a3d6d2d-506d-4e39-bfb0-1a03710fcce0,10.00,10.00,0.00,0.00010975,BTC,91116.17,,,
purchase,2025-07-16 06:07:03+00,UTC,settled,21c2238f-9e44-43a7-bab2-185ba6d88f4c,45.00,44.55,0.45,0.00037691,BTC,118197.98,,,
monthly_fee,2025-08-22 13:23:29+00,UTC,settled,,,,0.00,,USD,,,,
deposit,2024-12-26 12:06:43+00,UTC,settled,,,,,0.00098195,BTC,,Custodial Transfer from Fortress Trust,,
`

func TestParseSwanCSV_Basic(t *testing.T) {
	result, err := ParseSwanCSV(strings.NewReader(swanTestCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Errors) != 0 {
		t.Errorf("unexpected parse errors: %v", result.Errors)
	}

	// deposit + 2 purchases + monthly_fee + BTC deposit = 5 rows
	if len(result.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(result.Rows))
	}

	// First row: USD deposit
	dep := result.Rows[0]
	if dep.Type != "deposit" {
		t.Errorf("row 0 type = %q, want deposit", dep.Type)
	}
	if dep.AmountUSD != 70.00 {
		t.Errorf("row 0 USD = %f, want 70.00", dep.AmountUSD)
	}
	if dep.AmountSat != 0 {
		t.Errorf("row 0 sats = %d, want 0 (USD deposit)", dep.AmountSat)
	}

	// Second row: purchase with no fee
	buy1 := result.Rows[1]
	if buy1.Type != "purchase" {
		t.Errorf("row 1 type = %q, want purchase", buy1.Type)
	}
	if !buy1.IsPurchase() {
		t.Error("row 1 IsPurchase() = false, want true")
	}
	if buy1.AmountSat != 10975 {
		t.Errorf("row 1 sats = %d, want 10975", buy1.AmountSat)
	}
	if buy1.FeeUSD != 0 {
		t.Errorf("row 1 fee = %f, want 0", buy1.FeeUSD)
	}
	if buy1.TransactionID != "0a3d6d2d-506d-4e39-bfb0-1a03710fcce0" {
		t.Errorf("row 1 txID = %q", buy1.TransactionID)
	}

	// Third row: purchase with fee
	buy2 := result.Rows[2]
	if buy2.FeeUSD != 0.45 {
		t.Errorf("row 2 fee = %f, want 0.45", buy2.FeeUSD)
	}
	if buy2.TransactionUSD != 44.55 {
		t.Errorf("row 2 txUSD = %f, want 44.55", buy2.TransactionUSD)
	}
	if buy2.AmountUSD != 45.00 {
		t.Errorf("row 2 totalUSD = %f, want 45.00", buy2.AmountUSD)
	}
	if buy2.AmountSat != 37691 {
		t.Errorf("row 2 sats = %d, want 37691", buy2.AmountSat)
	}

	// Fourth row: monthly_fee
	fee := result.Rows[3]
	if fee.Type != "monthly_fee" {
		t.Errorf("row 3 type = %q, want monthly_fee", fee.Type)
	}

	// Fifth row: BTC deposit (custodial transfer)
	btcDep := result.Rows[4]
	if btcDep.Type != "deposit" {
		t.Errorf("row 4 type = %q, want deposit", btcDep.Type)
	}
	if btcDep.AmountSat != 98195 {
		t.Errorf("row 4 sats = %d, want 98195", btcDep.AmountSat)
	}
	if btcDep.AssetType != "BTC" {
		t.Errorf("row 4 asset = %q, want BTC", btcDep.AssetType)
	}
}

func TestParseSwanCSV_Empty(t *testing.T) {
	csv := `"Electric Solidus LLC"
Phone: 12183797926
Event,Date,Timezone,Status,Transaction ID,Total USD,Transaction USD,Fee USD,Unit Count,Asset Type,BTC Price,Address Label,USD Cost Basis,Acquisition Date
`
	result, err := ParseSwanCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestParseSwanCSV_BadHeader(t *testing.T) {
	csv := `"Electric Solidus LLC"
Phone: 12183797926
Wrong,Header,Format
`
	_, err := ParseSwanCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for bad header")
	}
}

func TestParseSwanCSV_MissingPreamble(t *testing.T) {
	_, err := ParseSwanCSV(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for missing preamble")
	}
}

func TestSwanRow_IsPurchase(t *testing.T) {
	if (SwanRow{Type: "purchase"}).IsPurchase() != true {
		t.Error("purchase should be IsPurchase")
	}
	if (SwanRow{Type: "deposit"}).IsPurchase() != false {
		t.Error("deposit should not be IsPurchase")
	}
}
