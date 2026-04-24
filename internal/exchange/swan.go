// Swan CSV format (as of 2026-04)
// "Deposits and Purchases" export from Swan Bitcoin.
// First two lines are company info (skipped), then the CSV header:
// Event,Date,Timezone,Status,Transaction ID,Total USD,Transaction USD,Fee USD,Unit Count,Asset Type,BTC Price,Address Label,USD Cost Basis,Acquisition Date
package exchange

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// SwanRow represents a single parsed row from a Swan CSV export.
type SwanRow struct {
	TransactionID string    `json:"TransactionID"`
	Date          time.Time `json:"Date"`
	Type          string    `json:"Type"`         // "purchase", "deposit", "monthly_fee"
	AmountBTC     float64   `json:"AmountBTC"`    // BTC received (purchases)
	AmountSat     int64     `json:"-"`            // derived from AmountBTC
	AmountUSD     float64   `json:"AmountUSD"`    // total USD (includes fee)
	TransactionUSD float64  `json:"TransactionUSD"` // USD actually applied to purchase
	FeeUSD        float64   `json:"FeeUSD"`
	BTCPrice      float64   `json:"BTCPrice"`
	CostBasisUSD  float64   `json:"CostBasisUSD"`
	AssetType     string    `json:"AssetType"`    // "BTC" or "USD"
	RawLine       string    `json:"-"`
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

var expectedSwanHeader = []string{
	"Event", "Date", "Timezone", "Status", "Transaction ID",
	"Total USD", "Transaction USD", "Fee USD", "Unit Count",
	"Asset Type", "BTC Price", "Address Label", "USD Cost Basis",
	"Acquisition Date",
}

const swanMinColumns = 14

// ParseSwanCSV parses a Swan "Deposits and Purchases" CSV export.
// The first two lines are company info and are skipped.
func ParseSwanCSV(r io.Reader) (*SwanResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip first two lines (company info)
	for i := 0; i < 2; i++ {
		_, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to read Swan CSV preamble line %d: %w", i+1, err)
		}
	}

	// Read and validate header (line 3)
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty CSV file")
		}
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	if err := validateSwanHeader(header); err != nil {
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

		if len(record) < swanMinColumns {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected %d columns, got %d", rowNum, swanMinColumns, len(record)))
			continue
		}

		row, err := parseSwanRow(record)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		// Skip non-settled rows
		if row.Type == "" {
			continue
		}

		row.RawLine = strings.Join(record, ",")
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

func validateSwanHeader(header []string) error {
	if len(header) < swanMinColumns {
		return fmt.Errorf("invalid Swan CSV format: expected %d columns, got %d", swanMinColumns, len(header))
	}
	for i, expected := range expectedSwanHeader {
		got := strings.TrimSpace(header[i])
		if !strings.EqualFold(got, expected) {
			return fmt.Errorf("invalid Swan CSV format: expected column %d to be %q, got %q", i+1, expected, got)
		}
	}
	return nil
}

var swanTimeFormats = []string{
	"2006-01-02 15:04:05-07",   // Swan's actual format (e.g. "2024-11-16 17:30:06+00")
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
}

func parseSwanRow(record []string) (SwanRow, error) {
	event := strings.TrimSpace(record[0])
	dateStr := strings.TrimSpace(record[1])
	status := strings.TrimSpace(record[3])
	txID := strings.TrimSpace(record[4])
	totalUSDStr := strings.TrimSpace(record[5])
	txUSDStr := strings.TrimSpace(record[6])
	feeUSDStr := strings.TrimSpace(record[7])
	unitCountStr := strings.TrimSpace(record[8])
	assetType := strings.TrimSpace(record[9])
	btcPriceStr := strings.TrimSpace(record[10])
	costBasisStr := strings.TrimSpace(record[12])

	// Skip non-settled
	if !strings.EqualFold(status, "settled") {
		return SwanRow{}, nil
	}

	// Parse date
	var date time.Time
	var parseErr error
	for _, format := range swanTimeFormats {
		date, parseErr = time.Parse(format, dateStr)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return SwanRow{}, fmt.Errorf("invalid date %q", dateStr)
	}

	totalUSD, err := parseOptionalFloat(totalUSDStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid total USD %q", totalUSDStr)
	}
	txUSD, err := parseOptionalFloat(txUSDStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid transaction USD %q", txUSDStr)
	}
	feeUSD, err := parseOptionalFloat(feeUSDStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid fee USD %q", feeUSDStr)
	}
	unitCount, err := parseOptionalFloat(unitCountStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid unit count %q", unitCountStr)
	}
	btcPrice, err := parseOptionalFloat(btcPriceStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid BTC price %q", btcPriceStr)
	}
	costBasis, err := parseOptionalFloat(costBasisStr)
	if err != nil {
		return SwanRow{}, fmt.Errorf("invalid cost basis %q", costBasisStr)
	}

	row := SwanRow{
		TransactionID:  txID,
		Date:           date,
		Type:           event,
		AmountUSD:      totalUSD,
		TransactionUSD: txUSD,
		FeeUSD:         feeUSD,
		AssetType:      assetType,
		BTCPrice:       btcPrice,
		CostBasisUSD:   costBasis,
	}

	// Only set BTC amount for BTC-denominated rows
	if strings.EqualFold(assetType, "BTC") {
		row.AmountBTC = unitCount
		row.AmountSat = int64(math.Round(unitCount * 1e8))
	}

	return row, nil
}
