// Swan Bitcoin CSV import (as of 2026-04)
//
// Swan exports three separate CSV files:
//   1. Deposits & Purchases ("transfers") — 2-line preamble, then CSV
//   2. CoinTracker Tax ("trades") — no preamble, just CSV
//   3. Withdrawals — 2-line preamble, then CSV
//
// All three are needed for an accurate balance. Purchases add BTC,
// BTC deposits add BTC, and withdrawals subtract BTC.
package exchange

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// SwanRow represents a single parsed row from any Swan CSV export.
type SwanRow struct {
	TransactionID  string    `json:"TransactionID"`
	Date           time.Time `json:"Date"`
	Type           string    `json:"Type"` // "purchase", "deposit", "withdrawal", "monthly_fee"
	AmountBTC      float64   `json:"AmountBTC"`
	AmountSat      int64     `json:"-"`
	AmountUSD      float64   `json:"AmountUSD"`
	FeeUSD         float64   `json:"FeeUSD"`
	BTCPrice       float64   `json:"BTCPrice"`
	CostBasisUSD   float64   `json:"CostBasisUSD"`
	RawLine        string    `json:"-"`
}

// SwanResult holds the result of parsing a Swan CSV.
type SwanResult struct {
	Rows   []SwanRow
	Errors []string
}

// IsPurchase returns true if the row represents a BTC purchase.
func (r SwanRow) IsPurchase() bool {
	return r.Type == "purchase"
}

// IsDeposit returns true if the row represents a BTC deposit into Swan.
func (r SwanRow) IsDeposit() bool {
	return r.Type == "deposit"
}

// IsWithdrawal returns true if the row represents a BTC withdrawal from Swan.
func (r SwanRow) IsWithdrawal() bool {
	return r.Type == "withdrawal"
}

// ---------------------------------------------------------------------------
// Trades CSV (CoinTracker Tax format) — purchases only, no preamble
// Header: Date,Received Quantity,Received Currency,Sent Quantity,Sent Currency,Fee Amount,Fee Currency,Tag
// ---------------------------------------------------------------------------

var expectedSwanTradesHeader = []string{
	"Date", "Received Quantity", "Received Currency", "Sent Quantity",
	"Sent Currency", "Fee Amount", "Fee Currency", "Tag",
}

// ParseSwanTradesCSV parses the "CoinTracker Tax" CSV (trades).
func ParseSwanTradesCSV(r io.Reader) (*SwanResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}
	if err := validateHeaderExact(header, expectedSwanTradesHeader, "Swan trades"); err != nil {
		return nil, err
	}

	result := &SwanResult{}
	for rowNum := 2; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}
		if len(record) < 8 {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected 8 columns, got %d", rowNum, len(record)))
			continue
		}

		row, err := parseSwanTradeRow(record)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		row.RawLine = strings.Join(record, ",")
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

var swanTradesTimeFormats = []string{
	"01/02/2006 15:04:05", // Swan's actual trades format
	"1/2/2006 15:04:05",
	"2006-01-02 15:04:05",
	time.RFC3339,
}

func parseSwanTradeRow(record []string) (SwanRow, error) {
	dateStr := strings.TrimSpace(record[0])
	recvQtyStr := strings.TrimSpace(record[1])
	recvCurrency := strings.TrimSpace(record[2])
	sentQtyStr := strings.TrimSpace(record[3])
	feeStr := strings.TrimSpace(record[5])

	date, err := parseMultiDate(dateStr, swanTradesTimeFormats)
	if err != nil {
		return SwanRow{}, err
	}

	recvQty, err := parseOptionalFloat(recvQtyStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid received quantity %q", recvQtyStr)
	}
	sentQty, err := parseOptionalFloat(sentQtyStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid sent quantity %q", sentQtyStr)
	}
	feeUSD, err := parseOptionalFloat(feeStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid fee %q", feeStr)
	}

	if !strings.EqualFold(recvCurrency, "BTC") || recvQty <= 0 {
		return SwanRow{}, fmt.Errorf("expected BTC purchase, got %s %f", recvCurrency, recvQty)
	}

	return SwanRow{
		Date:         date,
		Type:         "purchase",
		AmountBTC:    recvQty,
		AmountSat:    int64(math.Round(recvQty * 1e8)),
		AmountUSD:    sentQty + feeUSD, // total cost including fee
		FeeUSD:       feeUSD,
		CostBasisUSD: sentQty + feeUSD,
	}, nil
}

// ---------------------------------------------------------------------------
// Transfers CSV (Deposits & Purchases) — 2-line preamble
// Header: Event,Date,Timezone,Status,Transaction ID,Total USD,Transaction USD,
//         Fee USD,Unit Count,Asset Type,BTC Price,Address Label,USD Cost Basis,Acquisition Date
//
// We only extract BTC deposits from this file (purchases come from trades).
// ---------------------------------------------------------------------------

var expectedSwanTransfersHeader = []string{
	"Event", "Date", "Timezone", "Status", "Transaction ID",
	"Total USD", "Transaction USD", "Fee USD", "Unit Count",
	"Asset Type", "BTC Price", "Address Label", "USD Cost Basis",
	"Acquisition Date",
}

// ParseSwanTransfersCSV parses the "Deposits and Purchases" CSV.
// Only BTC deposits are extracted (purchases are handled by the trades file).
func ParseSwanTransfersCSV(r io.Reader) (*SwanResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip 2-line preamble
	for i := 0; i < 2; i++ {
		if _, err := reader.Read(); err != nil {
			return nil, fmt.Errorf("failed to read Swan CSV preamble line %d: %w", i+1, err)
		}
	}

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}
	if err := validateHeaderExact(header, expectedSwanTransfersHeader, "Swan transfers"); err != nil {
		return nil, err
	}

	result := &SwanResult{}
	for rowNum := 4; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}
		if len(record) < 14 {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected 14 columns, got %d", rowNum, len(record)))
			continue
		}

		event := strings.TrimSpace(record[0])
		status := strings.TrimSpace(record[3])
		assetType := strings.TrimSpace(record[9])

		// Only keep BTC deposits (not USD deposits, not purchases — those come from trades)
		if !strings.EqualFold(status, "settled") {
			continue
		}
		if !strings.EqualFold(event, "deposit") || !strings.EqualFold(assetType, "BTC") {
			continue
		}

		dateStr := strings.TrimSpace(record[1])
		date, err := parseMultiDate(dateStr, swanTimeFormats)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		unitCount, err := parseOptionalFloat(strings.TrimSpace(record[8]))
		if err != nil || unitCount <= 0 {
			continue
		}

		row := SwanRow{
			Date:      date,
			Type:      "deposit",
			AmountBTC: unitCount,
			AmountSat: int64(math.Round(unitCount * 1e8)),
			RawLine:   strings.Join(record, ","),
		}
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Withdrawals CSV — 2-line preamble
// Header: Created At,Timezone,Transaction ID,Executed At,Canceled At,Status,Bitcoin Amount,Automatic,IP Address
// ---------------------------------------------------------------------------

var expectedSwanWithdrawalsHeader = []string{
	"Created At", "Timezone", "Transaction ID", "Executed At",
	"Canceled At", "Status", "Bitcoin Amount", "Automatic", "IP Address",
}

// ParseSwanWithdrawalsCSV parses the "Withdrawals" CSV.
func ParseSwanWithdrawalsCSV(r io.Reader) (*SwanResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip 2-line preamble
	for i := 0; i < 2; i++ {
		if _, err := reader.Read(); err != nil {
			return nil, fmt.Errorf("failed to read Swan CSV preamble line %d: %w", i+1, err)
		}
	}

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}
	if err := validateHeaderExact(header, expectedSwanWithdrawalsHeader, "Swan withdrawals"); err != nil {
		return nil, err
	}

	result := &SwanResult{}
	for rowNum := 4; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}
		if len(record) < 9 {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected 9 columns, got %d", rowNum, len(record)))
			continue
		}

		status := strings.TrimSpace(record[5])
		if !strings.EqualFold(status, "settled") {
			continue
		}

		txID := strings.TrimSpace(record[2])
		// Use Executed At (column 3) for the actual withdrawal time
		dateStr := strings.TrimSpace(record[3])
		if dateStr == "" {
			dateStr = strings.TrimSpace(record[0]) // fallback to Created At
		}

		date, err := parseMultiDate(dateStr, swanTimeFormats)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		btcAmt, err := parseOptionalFloat(strings.TrimSpace(record[6]))
		if err != nil || btcAmt <= 0 {
			continue
		}

		row := SwanRow{
			TransactionID: txID,
			Date:          date,
			Type:          "withdrawal",
			AmountBTC:     -btcAmt, // negative — BTC leaving
			AmountSat:     -int64(math.Round(btcAmt * 1e8)),
			RawLine:       strings.Join(record, ","),
		}
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var swanTimeFormats = []string{
	"2006-01-02 15:04:05-07",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
}

func validateHeaderExact(header, expected []string, label string) error {
	if len(header) < len(expected) {
		return fmt.Errorf("invalid %s CSV format: expected %d columns, got %d", label, len(expected), len(header))
	}
	for i, exp := range expected {
		got := strings.TrimSpace(header[i])
		if !strings.EqualFold(got, exp) {
			return fmt.Errorf("invalid %s CSV format: expected column %d to be %q, got %q", label, i+1, exp, got)
		}
	}
	return nil
}

func parseMultiDate(s string, formats []string) (time.Time, error) {
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", s)
}
