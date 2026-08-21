// Coinbase retail CSV format (as of 2026-03)
// CSV has preamble lines before the header. Header row starts with "ID".
// Expected header: ID,Timestamp,Transaction Type,Asset,Quantity Transacted,
//   Price Currency,Price at Transaction,Subtotal,Total (inclusive of fees and/or spread),
//   Fees and/or Spread,Notes
package exchange

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// CoinbaseRow represents a single parsed row from a Coinbase CSV export or API response.
// JSON field names match StrikeRow/RiverRow so ExchangeSummary/ExchangeBalance queries work unchanged.
type CoinbaseRow struct {
	TransactionID string    `json:"-"`            // Coinbase UUID from API; empty for CSV rows
	Date          time.Time `json:"Date"`
	Type          string    `json:"Type"`         // "buy", "sale", "send", "receive"
	AmountBTC     float64   `json:"AmountBTC"`    // always positive; direction implied by Type
	AmountSat     int64     `json:"-"`            // derived from AmountBTC
	AmountUSD     float64   `json:"AmountUSD"`
	FeeUSD        float64   `json:"FeeUSD"`
	CostBasisUSD  float64   `json:"CostBasisUSD"` // for buys: total USD spent
	RawLine       string    `json:"-"`
}

// CoinbaseResult holds the result of parsing a Coinbase CSV.
type CoinbaseResult struct {
	Rows    []CoinbaseRow
	Errors  []string
	Skipped int // count of non-BTC rows skipped
}

// IsPurchase returns true if the row represents a BTC purchase.
func (r CoinbaseRow) IsPurchase() bool {
	return r.Type == "buy"
}

// Coinbase CSV column indices (after header).
const (
	cbColTimestamp = 1
	cbColTxType   = 2
	cbColAsset    = 3
	cbColQuantity = 4
	cbColSubtotal = 7
	cbColTotal    = 8
	cbColFees     = 9
	cbMinColumns  = 11
)

const coinbaseTimeFormat = "2006-01-02 15:04:05 MST"

// parseCoinbaseUSD strips $ prefix and handles negative USD amounts like "-$179.32".
func parseCoinbaseUSD(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, nil
	}

	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}

	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if negative {
		v = -v
	}
	return v, nil
}

// ParseCoinbaseCSV parses a Coinbase retail transaction export CSV.
// It skips preamble lines until finding the header row (starts with "ID"),
// filters to BTC-only rows, and maps transaction types accordingly.
// Invalid rows are recorded as errors but do not halt parsing.
func ParseCoinbaseCSV(r io.Reader) (*CoinbaseResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1 // handle Notes field with commas
	reader.LazyQuotes = true

	// Skip preamble lines until we find the header row starting with "ID".
	var header []string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			return nil, fmt.Errorf("no header row found in Coinbase CSV")
		}
		if err != nil {
			// Skip malformed preamble lines.
			continue
		}
		if len(record) > 0 && strings.TrimSpace(record[0]) == "ID" {
			header = record
			break
		}
	}

	if len(header) < cbMinColumns {
		return nil, fmt.Errorf("invalid Coinbase CSV format: expected at least %d columns, got %d", cbMinColumns, len(header))
	}

	result := &CoinbaseResult{}

	for rowNum := 1; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		if len(record) < cbMinColumns {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected at least %d columns, got %d", rowNum, cbMinColumns, len(record)))
			continue
		}

		asset := strings.TrimSpace(record[cbColAsset])
		if !strings.EqualFold(asset, "BTC") {
			result.Skipped++
			continue
		}

		row, err := parseCoinbaseRow(record)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		row.RawLine = strings.Join(record, ",")
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

func parseCoinbaseRow(record []string) (CoinbaseRow, error) {
	dateStr := strings.TrimSpace(record[cbColTimestamp])
	txType := strings.TrimSpace(record[cbColTxType])
	qtyStr := strings.TrimSpace(record[cbColQuantity])
	subtotalStr := strings.TrimSpace(record[cbColSubtotal])
	totalStr := strings.TrimSpace(record[cbColTotal])
	feesStr := strings.TrimSpace(record[cbColFees])

	// Parse date.
	date, err := time.Parse(coinbaseTimeFormat, dateStr)
	if err != nil {
		return CoinbaseRow{}, fmt.Errorf("invalid date %q: %w", dateStr, err)
	}

	// Parse quantity (can be negative for sends).
	qty, err := parseOptionalFloat(qtyStr)
	if err != nil {
		return CoinbaseRow{}, fmt.Errorf("invalid quantity %q: %w", qtyStr, err)
	}

	// Parse USD amounts (may have $ prefix).
	subtotal, err := parseCoinbaseUSD(subtotalStr)
	if err != nil {
		return CoinbaseRow{}, fmt.Errorf("invalid subtotal %q: %w", subtotalStr, err)
	}

	total, err := parseCoinbaseUSD(totalStr)
	if err != nil {
		return CoinbaseRow{}, fmt.Errorf("invalid total %q: %w", totalStr, err)
	}

	fees, err := parseCoinbaseUSD(feesStr)
	if err != nil {
		return CoinbaseRow{}, fmt.Errorf("invalid fees %q: %w", feesStr, err)
	}

	row := CoinbaseRow{Date: date}

	switch strings.ToLower(txType) {
	case "buy", "advanced trade buy":
		row.Type = "buy"
		row.AmountBTC = math.Abs(qty)
		row.CostBasisUSD = math.Abs(total)
		row.FeeUSD = math.Abs(fees)

	case "sell", "advanced trade sell":
		row.Type = "sale"
		row.AmountBTC = -math.Abs(qty)
		row.AmountUSD = math.Abs(subtotal)
		row.FeeUSD = math.Abs(fees)

	case "send":
		row.Type = "send"
		row.AmountBTC = qty // already negative

	case "receive":
		row.Type = "receive"
		row.AmountBTC = qty // positive

	case "convert":
		// Convert with Asset=BTC means selling BTC (negative quantity).
		row.Type = "sale"
		row.AmountBTC = qty // negative
		row.AmountUSD = math.Abs(subtotal)
		row.FeeUSD = math.Abs(fees)

	case "staking income":
		row.Type = "receive"
		row.AmountBTC = qty
		row.CostBasisUSD = math.Abs(subtotal)

	default:
		return CoinbaseRow{}, fmt.Errorf("unknown BTC transaction type %q", txType)
	}

	row.AmountSat = int64(math.Round(row.AmountBTC * 1e8))

	return row, nil
}
