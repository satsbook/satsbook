package exchange

import (
	"strings"
	"testing"
)

const validCSV = `Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status
tx-001,2024-06-15T10:30:00Z,Purchase,0.00100000,67.00,0.50,Completed
tx-002,2024-06-16T12:00:00Z,Withdrawal,0.00050000,33.50,1.00,Completed
tx-003,2024-06-17T14:00:00Z,Deposit,0.00200000,134.00,0.00,Completed
tx-004,2024-06-18T09:00:00Z,Purchase,0.00010000,6.70,0.10,Pending
tx-005,2024-06-19T08:00:00Z,Buy,0.50000000,33500.00,25.00,Completed
`

func TestParseStrikeCSV_Valid(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(result.Rows))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %v", result.Errors)
	}

	// Check first row
	r := result.Rows[0]
	if r.TransactionID != "tx-001" {
		t.Errorf("expected tx-001, got %q", r.TransactionID)
	}
	if r.Type != "Purchase" {
		t.Errorf("expected Purchase, got %q", r.Type)
	}
	if r.AmountSat != 100000 {
		t.Errorf("expected 100000 sats (0.001 BTC), got %d", r.AmountSat)
	}
	if r.AmountUSD != 67.0 {
		t.Errorf("expected 67.0 USD, got %f", r.AmountUSD)
	}
	if r.FeeUSD != 0.5 {
		t.Errorf("expected 0.5 fee, got %f", r.FeeUSD)
	}
	if r.Status != "Completed" {
		t.Errorf("expected Completed, got %q", r.Status)
	}
}

func TestParseStrikeCSV_IsPurchase(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// tx-001: Purchase + Completed = true
	if !result.Rows[0].IsPurchase() {
		t.Error("tx-001 should be a purchase")
	}
	// tx-002: Withdrawal = false
	if result.Rows[1].IsPurchase() {
		t.Error("tx-002 (Withdrawal) should not be a purchase")
	}
	// tx-003: Deposit = false
	if result.Rows[2].IsPurchase() {
		t.Error("tx-003 (Deposit) should not be a purchase")
	}
	// tx-004: Purchase + Pending = false
	if result.Rows[3].IsPurchase() {
		t.Error("tx-004 (Pending) should not be a purchase")
	}
	// tx-005: Buy + Completed = true
	if !result.Rows[4].IsPurchase() {
		t.Error("tx-005 (Buy) should be a purchase")
	}
}

func TestParseStrikeCSV_SatConversion(t *testing.T) {
	tests := []struct {
		btc      string
		expected int64
	}{
		{"0.00000001", 1},
		{"0.00010000", 10000},
		{"0.00100000", 100000},
		{"1.00000000", 100000000},
		{"0.50000000", 50000000},
		{"0.12345678", 12345678},
	}

	for _, tt := range tests {
		csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1,2024-01-01T00:00:00Z,Purchase," + tt.btc + ",100.00,0.00,Completed\n"
		result, err := ParseStrikeCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", tt.btc, err)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("expected 1 row for %s", tt.btc)
		}
		if result.Rows[0].AmountSat != tt.expected {
			t.Errorf("BTC %s: expected %d sats, got %d", tt.btc, tt.expected, result.Rows[0].AmountSat)
		}
	}
}

func TestParseStrikeCSV_WrongHeader(t *testing.T) {
	csv := "ID,Date,Kind,BTC,USD,Fee,Status\ntx-1,2024-01-01,Buy,0.001,67,0.5,Done\n"
	_, err := ParseStrikeCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for wrong header")
	}
	if !strings.Contains(err.Error(), "invalid Strike CSV format") {
		t.Errorf("expected 'invalid Strike CSV format', got: %v", err)
	}
}

func TestParseStrikeCSV_EmptyFile(t *testing.T) {
	_, err := ParseStrikeCSV(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "empty CSV file") {
		t.Errorf("expected 'empty CSV file', got: %v", err)
	}
}

func TestParseStrikeCSV_HeaderOnly(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestParseStrikeCSV_MalformedRows(t *testing.T) {
	csv := `Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status
tx-001,2024-06-15T10:30:00Z,Purchase,0.001,67.00,0.50,Completed
tx-002,bad-date,Purchase,0.001,67.00,0.50,Completed
tx-003,2024-06-15T10:30:00Z,Purchase,notanumber,67.00,0.50,Completed
,2024-06-15T10:30:00Z,Purchase,0.001,67.00,0.50,Completed
tx-005,2024-06-15T10:30:00Z,Purchase,0.001,bad,0.50,Completed
tx-006,2024-06-15T10:30:00Z,Purchase,0.001,67.00,bad,Completed
`

	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 valid row, got %d", len(result.Rows))
	}
	if len(result.Errors) != 5 {
		t.Errorf("expected 5 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestParseStrikeCSV_TooFewColumns(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-001,2024-01-01,Purchase\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for short row, got %d", len(result.Errors))
	}
}

func TestParseStrikeCSV_USDWithSymbols(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1,2024-01-01T00:00:00Z,Purchase,0.001,\"$1,234.56\",$0.50,Completed\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (errors: %v)", len(result.Rows), result.Errors)
	}
	if result.Rows[0].AmountUSD != 1234.56 {
		t.Errorf("expected 1234.56, got %f", result.Rows[0].AmountUSD)
	}
}

func TestParseStrikeCSV_EmptyUSDFee(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1,2024-01-01T00:00:00Z,Deposit,0.001,-,-,Completed\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (errors: %v)", len(result.Rows), result.Errors)
	}
	if result.Rows[0].AmountUSD != 0 {
		t.Errorf("expected 0 for '-', got %f", result.Rows[0].AmountUSD)
	}
	if result.Rows[0].FeeUSD != 0 {
		t.Errorf("expected 0 for '-', got %f", result.Rows[0].FeeUSD)
	}
}

func TestParseStrikeCSV_DateFormats(t *testing.T) {
	formats := []string{
		"2024-06-15T10:30:00Z",
		"2024-06-15T10:30:00-05:00",
		"2024-06-15 10:30:00",
		"06/15/2024 10:30:00",
		"06/15/2024",
		"2024-06-15",
	}

	for _, dateStr := range formats {
		csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1," + dateStr + ",Purchase,0.001,67.00,0.50,Completed\n"
		result, err := ParseStrikeCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("unexpected error for date %q: %v", dateStr, err)
		}
		if len(result.Rows) != 1 {
			t.Errorf("expected 1 row for date %q, got %d (errors: %v)", dateStr, len(result.Rows), result.Errors)
		}
	}
}

func TestParseStrikeCSV_NegativeAmount(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1,2024-01-01T00:00:00Z,Withdrawal,-0.001,-67.00,1.00,Completed\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0].AmountSat != -100000 {
		t.Errorf("expected -100000 sats, got %d", result.Rows[0].AmountSat)
	}
}

func TestParseStrikeCSV_RawLine(t *testing.T) {
	csv := "Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status\ntx-1,2024-01-01T00:00:00Z,Purchase,0.001,67.00,0.50,Completed\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Rows[0].RawLine == "" {
		t.Error("expected RawLine to be populated")
	}
}

func TestParseStrikeCSV_CaseInsensitiveHeader(t *testing.T) {
	csv := "transaction id,date,type,amount btc,amount usd,fee usd,status\ntx-1,2024-01-01T00:00:00Z,Purchase,0.001,67.00,0.50,Completed\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(result.Rows))
	}
}

func TestParseUSD(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"67.00", 67.0},
		{"$67.00", 67.0},
		{"$1,234.56", 1234.56},
		{"-", 0},
		{"", 0},
		{"0.00", 0},
	}
	for _, tt := range tests {
		got, err := parseUSD(tt.input)
		if err != nil {
			t.Errorf("parseUSD(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseUSD(%q) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func TestValidateHeader_TooFewColumns(t *testing.T) {
	err := validateHeader([]string{"Transaction ID", "Date"})
	if err == nil {
		t.Fatal("expected error for too few columns")
	}
}
