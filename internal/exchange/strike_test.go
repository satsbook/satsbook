package exchange

import (
	"strings"
	"testing"
)

const strikeHeader = "Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note"

const validCSV = strikeHeader + `
fd217b15-9b68-4a3a-8961-b702fdebb2e2,Jan 01 2026 03:09:55,Receive,,,0.02287023,,,,bc1qgcjf5yva5fzvys6e0c9vfxr3wcj2tgswudz3eu,,658bc49a9865814e87bd9f69a38f68be6b4e7b1c52f28e83ce902f5b70d65cd3,
2acfbd5b-52cd-4f9c-9cda-8f3660617ae6,Jan 02 2026 08:20:58,Sale,1401.67,11.16,-0.01590447,,88832.26,,,Bill pay to APPLECARD GSBANK,,
tx-003,Jan 03 2026 10:00:00,Purchase,500.00,3.99,0.00530000,,94339.62,500.00,,,some-hash,
tx-004,Jan 04 2026 12:00:00,Buy,100.00,0.79,0.00106000,,94339.62,100.00,,,another-hash,
tx-005,Jan 05 2026 05:11:59,Withdrawal,-110.97,,,,,,,Bill pay to City,,
`

func TestParseStrikeCSV_Valid(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d (errors: %v)", len(result.Rows), result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %v", result.Errors)
	}

	// Check receive row
	r := result.Rows[0]
	if r.TransactionID != "fd217b15-9b68-4a3a-8961-b702fdebb2e2" {
		t.Errorf("expected fd217b15..., got %q", r.TransactionID)
	}
	if r.Type != "Receive" {
		t.Errorf("expected Receive, got %q", r.Type)
	}
	if r.AmountSat != 2287023 {
		t.Errorf("expected 2287023 sats, got %d", r.AmountSat)
	}
	if r.AmountUSD != 0 {
		t.Errorf("expected 0 USD for receive, got %f", r.AmountUSD)
	}

	// Check sale row
	s := result.Rows[1]
	if s.Type != "Sale" {
		t.Errorf("expected Sale, got %q", s.Type)
	}
	if s.AmountUSD != 1401.67 {
		t.Errorf("expected 1401.67 USD, got %f", s.AmountUSD)
	}
	if s.FeeUSD != 11.16 {
		t.Errorf("expected 11.16 fee, got %f", s.FeeUSD)
	}
	if s.BTCPrice != 88832.26 {
		t.Errorf("expected BTC price 88832.26, got %f", s.BTCPrice)
	}

	// Check purchase row
	p := result.Rows[2]
	if p.Type != "Purchase" {
		t.Errorf("expected Purchase, got %q", p.Type)
	}
	if p.AmountSat != 530000 {
		t.Errorf("expected 530000 sats, got %d", p.AmountSat)
	}
	if p.CostBasisUSD != 500.0 {
		t.Errorf("expected cost basis 500.0, got %f", p.CostBasisUSD)
	}
}

func TestParseStrikeCSV_IsPurchase(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		idx      int
		typ      string
		expected bool
	}{
		{0, "Receive", false},
		{1, "Sale", false},
		{2, "Purchase", true},
		{3, "Buy", true},
		{4, "Withdrawal", false},
	}
	for _, tt := range tests {
		if result.Rows[tt.idx].IsPurchase() != tt.expected {
			t.Errorf("row %d (%s): IsPurchase()=%v, want %v", tt.idx, tt.typ, !tt.expected, tt.expected)
		}
	}
}

func TestParseStrikeCSV_IsReceive(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Rows[0].IsReceive() {
		t.Error("row 0 should be a receive")
	}
	if result.Rows[1].IsReceive() {
		t.Error("row 1 (Sale) should not be a receive")
	}
}

func TestParseStrikeCSV_IsSale(t *testing.T) {
	result, err := ParseStrikeCSV(strings.NewReader(validCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Rows[1].IsSale() {
		t.Error("row 1 should be a sale")
	}
	if result.Rows[0].IsSale() {
		t.Error("row 0 (Receive) should not be a sale")
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
		csv := strikeHeader + "\ntx-1,Jan 01 2026 00:00:00,Purchase,100.00,0.00," + tt.btc + ",,94000.00,100.00,,,,\n"
		result, err := ParseStrikeCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", tt.btc, err)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("expected 1 row for %s, got %d (errors: %v)", tt.btc, len(result.Rows), result.Errors)
		}
		if result.Rows[0].AmountSat != tt.expected {
			t.Errorf("BTC %s: expected %d sats, got %d", tt.btc, tt.expected, result.Rows[0].AmountSat)
		}
	}
}

func TestParseStrikeCSV_WrongHeader(t *testing.T) {
	csv := "ID,Date,Kind,USD,Fee,BTC,FeeBTC,Price,Basis,Dest,Desc,Hash,Note\ntx-1,Jan 01 2026 00:00:00,Buy,100,0,0.001,,,,,,,\n"
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
	csv := strikeHeader + "\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestParseStrikeCSV_MalformedRows(t *testing.T) {
	csv := strikeHeader + `
tx-001,Jan 01 2026 10:00:00,Purchase,100.00,1.00,0.001,,94000.00,100.00,,,,
tx-002,bad-date,Purchase,100.00,1.00,0.001,,94000.00,,,,,
tx-003,Jan 01 2026 10:00:00,Purchase,100.00,1.00,notanumber,,94000.00,,,,,
,Jan 01 2026 10:00:00,Purchase,100.00,1.00,0.001,,94000.00,,,,,
tx-005,Jan 01 2026 10:00:00,Purchase,bad,1.00,0.001,,94000.00,,,,,
tx-006,Jan 01 2026 10:00:00,Purchase,100.00,bad,0.001,,94000.00,,,,,
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
	csv := strikeHeader + "\ntx-001,Jan 01 2026 00:00:00,Purchase\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for short row, got %d", len(result.Errors))
	}
}

func TestParseStrikeCSV_EmptyOptionalFields(t *testing.T) {
	// Receive row with many empty fields — matches real Strike format
	csv := strikeHeader + "\ntx-1,Jan 01 2026 03:09:55,Receive,,,0.02287023,,,,bc1qaddr,,txhash123,\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (errors: %v)", len(result.Rows), result.Errors)
	}
	r := result.Rows[0]
	if r.AmountUSD != 0 {
		t.Errorf("expected 0 USD for empty field, got %f", r.AmountUSD)
	}
	if r.FeeUSD != 0 {
		t.Errorf("expected 0 fee for empty field, got %f", r.FeeUSD)
	}
	if r.Destination != "bc1qaddr" {
		t.Errorf("expected bc1qaddr, got %q", r.Destination)
	}
	if r.TransactionHash != "txhash123" {
		t.Errorf("expected txhash123, got %q", r.TransactionHash)
	}
}

func TestParseStrikeCSV_NegativeAmount(t *testing.T) {
	csv := strikeHeader + "\ntx-1,Jan 01 2026 00:00:00,Sale,1401.67,11.16,-0.01590447,,88832.26,,,Bill pay,,\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0].AmountSat != -1590447 {
		t.Errorf("expected -1590447 sats, got %d", result.Rows[0].AmountSat)
	}
}

func TestParseStrikeCSV_DateFormats(t *testing.T) {
	formats := []string{
		"Jan 01 2026 10:30:00",
		"Jan 1 2026 10:30:00",
		"2024-06-15T10:30:00Z",
		"2024-06-15T10:30:00-05:00",
		"2024-06-15 10:30:00",
		"06/15/2024 10:30:00",
		"06/15/2024",
		"2024-06-15",
	}

	for _, dateStr := range formats {
		csv := strikeHeader + "\ntx-1," + dateStr + ",Purchase,100.00,1.00,0.001,,94000.00,100.00,,,,\n"
		result, err := ParseStrikeCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("unexpected error for date %q: %v", dateStr, err)
		}
		if len(result.Rows) != 1 {
			t.Errorf("expected 1 row for date %q, got %d (errors: %v)", dateStr, len(result.Rows), result.Errors)
		}
	}
}

func TestParseStrikeCSV_RawLine(t *testing.T) {
	csv := strikeHeader + "\ntx-1,Jan 01 2026 00:00:00,Purchase,100.00,1.00,0.001,,94000.00,100.00,,,,\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Rows[0].RawLine == "" {
		t.Error("expected RawLine to be populated")
	}
}

func TestParseStrikeCSV_CaseInsensitiveHeader(t *testing.T) {
	csv := "reference,date & time (utc),transaction type,amount usd,fee usd,amount btc,fee btc,btc price,cost basis (usd),destination,description,transaction hash,note\ntx-1,Jan 01 2026 00:00:00,Purchase,100.00,1.00,0.001,,94000.00,100.00,,,,\n"
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(result.Rows))
	}
}

func TestParseStrikeCSV_USDWithSymbols(t *testing.T) {
	csv := strikeHeader + "\ntx-1,Jan 01 2026 00:00:00,Purchase,\"$1,234.56\",$0.50,0.001,,94000.00,,,,,\n"
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
		{"-1401.67", -1401.67},
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

func TestParseOptionalFloat(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"0.001", 0.001},
		{"", 0},
		{"-", 0},
		{"-0.015", -0.015},
		{"94339.62", 94339.62},
	}
	for _, tt := range tests {
		got, err := parseOptionalFloat(tt.input)
		if err != nil {
			t.Errorf("parseOptionalFloat(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseOptionalFloat(%q) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func TestValidateHeader_TooFewColumns(t *testing.T) {
	err := validateHeader([]string{"Reference", "Date & Time (UTC)"})
	if err == nil {
		t.Fatal("expected error for too few columns")
	}
}

func TestStrikeBalanceMath(t *testing.T) {
	// Realistic mix: 2 purchases, 1 receive, 1 sale, 1 withdrawal
	// Net BTC = purchases + receives - sales
	// 530000 + 106000 + 2287023 - 1590447 = 1332576
	csv := strikeHeader + `
fd217b15-9b68-4a3a-8961-b702fdebb2e2,Jan 01 2026 03:09:55,Receive,,,0.02287023,,,,bc1qaddr,,txhash1,
2acfbd5b-52cd-4f9c-9cda-8f3660617ae6,Jan 02 2026 08:20:58,Sale,1401.67,11.16,-0.01590447,,88832.26,,,Bill pay,,
tx-003,Jan 03 2026 10:00:00,Purchase,500.00,3.99,0.00530000,,94339.62,500.00,,,hash3,
tx-004,Jan 04 2026 12:00:00,Buy,100.00,0.79,0.00106000,,94339.62,100.00,,,hash4,
tx-005,Jan 05 2026 05:11:59,Withdrawal,-110.97,,,,,,,Bill pay to City,,
`
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var totalSat int64
	for _, r := range result.Rows {
		totalSat += r.AmountSat
	}

	// Withdrawal has 0 BTC, so only BTC rows matter:
	// 2287023 + (-1590447) + 530000 + 106000 = 1332576
	if totalSat != 1332576 {
		t.Errorf("net balance = %d sats, want 1332576", totalSat)
	}
}

func TestStrikeCostBasisMath(t *testing.T) {
	// Verify cost basis sums correctly for purchases only
	csv := strikeHeader + `
tx-001,Jan 01 2026 10:00:00,Purchase,500.00,3.99,0.00530000,,94339.62,500.00,,,hash1,
tx-002,Jan 02 2026 10:00:00,Buy,100.00,0.79,0.00106000,,94339.62,100.00,,,hash2,
tx-003,Jan 03 2026 10:00:00,Receive,,,0.01000000,,,,bc1qaddr,,hash3,
`
	result, err := ParseStrikeCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var totalCostBasis float64
	var totalPurchaseSat int64
	for _, r := range result.Rows {
		if r.IsPurchase() {
			totalCostBasis += r.CostBasisUSD
			totalPurchaseSat += r.AmountSat
		}
	}

	if totalCostBasis != 600.00 {
		t.Errorf("total cost basis = %f, want 600.00", totalCostBasis)
	}
	if totalPurchaseSat != 636000 {
		t.Errorf("total purchase sats = %d, want 636000", totalPurchaseSat)
	}
}

func TestStrikeRowClassify(t *testing.T) {
	cases := []struct {
		typ  string
		want StrikeClassification
	}{
		{"Purchase", ClassAcquisition},
		{"buy", ClassAcquisition},
		{"Receive", ClassAcquisition},
		{"Sale", ClassDisposal},
		{"Withdraw", ClassIgnored},
		{"Transfer", ClassIgnored},
		{"Send", ClassIgnored},
		{"unknown_future_type", ClassIgnored},
		{"", ClassIgnored},
	}
	for _, tc := range cases {
		row := StrikeRow{Type: tc.typ}
		got := row.Classify()
		if got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.typ, got, tc.want)
		}
		isIgnored := row.IsIgnored()
		wantIgnored := tc.want == ClassIgnored
		if isIgnored != wantIgnored {
			t.Errorf("IsIgnored(%q) = %v, want %v", tc.typ, isIgnored, wantIgnored)
		}
	}
}

func TestStrikeRowClassify_AmountFallback(t *testing.T) {
	// Unrecognized types are classified by BTC amount direction.
	cases := []struct {
		typ       string
		amountBTC float64
		want      StrikeClassification
		ignored   bool
	}{
		// Send: negative = disposal, positive = acquisition (reversal), zero = ignored
		{"Send", -0.001, ClassDisposal, false},
		{"Send", 0.001, ClassAcquisition, false},
		{"Send", 0, ClassIgnored, true},
		// Loan collateral: positive = retrieval (acquisition), negative = pledge (disposal)
		{"Loan collateral", 0.055, ClassAcquisition, false},
		{"Loan collateral", -0.055, ClassDisposal, false},
		{"Loan collateral", 0, ClassIgnored, true},
		// 0-BTC AND 0-USD rows are truly ignored (no accounting impact at all)
		{"Deposit", 0, ClassIgnored, true},
		{"Withdrawal", 0, ClassIgnored, true},
		{"Line of credit draw", 0, ClassIgnored, true},
		{"Principal payment", 0, ClassIgnored, true},
		{"Interest payment", 0, ClassIgnored, true},
	}
	for _, tc := range cases {
		row := StrikeRow{Type: tc.typ, AmountBTC: tc.amountBTC}
		got := row.Classify()
		if got != tc.want {
			t.Errorf("Classify(%q, btc=%g) = %q, want %q", tc.typ, tc.amountBTC, got, tc.want)
		}
		if row.IsIgnored() != tc.ignored {
			t.Errorf("IsIgnored(%q, btc=%g) = %v, want %v", tc.typ, tc.amountBTC, row.IsIgnored(), tc.ignored)
		}
	}
}

func TestStrikeRowClassify_USDFallback(t *testing.T) {
	// When BTC is 0, USD direction decides classification.
	// This ensures fiat-only rows (bill pay, credit draws, deposits)
	// are imported for Monarch sync, matching pre-preview-commit behaviour.
	cases := []struct {
		typ       string
		amountUSD float64
		want      StrikeClassification
		ignored   bool
	}{
		{"Withdrawal", -95.00, ClassDisposal, false},   // bill pay
		{"Deposit", 100.00, ClassAcquisition, false},   // fiat deposit
		{"Line of credit draw", 405.42, ClassAcquisition, false},
		{"Principal payment", -316.58, ClassDisposal, false},
		{"Interest payment", -69.67, ClassDisposal, false},
		// Both 0 — truly ignored
		{"Withdrawal", 0, ClassIgnored, true},
		{"Deposit", 0, ClassIgnored, true},
	}
	for _, tc := range cases {
		row := StrikeRow{Type: tc.typ, AmountBTC: 0, AmountUSD: tc.amountUSD}
		got := row.Classify()
		if got != tc.want {
			t.Errorf("Classify(%q, usd=%g) = %q, want %q", tc.typ, tc.amountUSD, got, tc.want)
		}
		if row.IsIgnored() != tc.ignored {
			t.Errorf("IsIgnored(%q, usd=%g) = %v, want %v", tc.typ, tc.amountUSD, row.IsIgnored(), tc.ignored)
		}
	}
}

func TestParseStrikeRawLine(t *testing.T) {
	// A valid raw line that was originally parsed and joined
	line := `REF001,Jan 15 2025 10:00:00,Purchase,500.00,2.50,0.00500000,0.00002500,100000.00,500.00,wallet123,Buy BTC,txhash001,`
	row, err := ParseStrikeRawLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TransactionID != "REF001" {
		t.Errorf("expected TransactionID REF001, got %q", row.TransactionID)
	}
	if row.Type != "Purchase" {
		t.Errorf("expected Type Purchase, got %q", row.Type)
	}
	if row.Classify() != ClassAcquisition {
		t.Errorf("expected ClassAcquisition, got %q", row.Classify())
	}
}

func TestParseStrikeRawLine_TooFewColumns(t *testing.T) {
	_, err := ParseStrikeRawLine("col1,col2")
	if err == nil {
		t.Error("expected error for too few columns, got nil")
	}
}
