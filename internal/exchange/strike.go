// Strike CSV format v1 (as of 2026-03)
// Expected header: Transaction ID,Date,Type,Amount BTC,Amount USD,Fee USD,Status
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

// StrikeRow represents a single parsed row from a Strike CSV export.
type StrikeRow struct {
	TransactionID string
	Date          time.Time
	Type          string
	AmountSat     int64
	AmountBTC     float64
	AmountUSD     float64
	FeeUSD        float64
	Status        string
	RawLine       string
}

// StrikeResult holds the result of parsing a Strike CSV.
type StrikeResult struct {
	Rows   []StrikeRow
	Errors []string
}

// IsPurchase returns true if the row represents a completed BTC purchase.
func (r StrikeRow) IsPurchase() bool {
	t := strings.ToLower(r.Type)
	return (t == "purchase" || t == "buy") && strings.EqualFold(r.Status, "completed")
}

var expectedStrikeHeader = []string{
	"Transaction ID", "Date", "Type", "Amount BTC", "Amount USD", "Fee USD", "Status",
}

// ParseStrikeCSV parses a Strike transaction export CSV.
// It validates the header strictly and parses each data row.
// Invalid rows are recorded as errors but do not halt parsing.
func ParseStrikeCSV(r io.Reader) (*StrikeResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1 // allow variable fields; we validate manually

	// Read and validate header
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty CSV file")
		}
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	if err := validateHeader(header); err != nil {
		return nil, err
	}

	result := &StrikeResult{}

	for rowNum := 2; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		if len(record) < 7 {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected 7 columns, got %d", rowNum, len(record)))
			continue
		}

		txID := strings.TrimSpace(record[0])
		if txID == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: empty transaction ID", rowNum))
			continue
		}

		row, err := parseStrikeRow(record)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		row.RawLine = strings.Join(record, ",")
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

func validateHeader(header []string) error {
	if len(header) < 7 {
		return fmt.Errorf("invalid Strike CSV format: expected %d columns, got %d", len(expectedStrikeHeader), len(header))
	}
	for i, expected := range expectedStrikeHeader {
		got := strings.TrimSpace(header[i])
		if !strings.EqualFold(got, expected) {
			return fmt.Errorf("invalid Strike CSV format: expected column %d to be %q, got %q", i+1, expected, got)
		}
	}
	return nil
}

// strikeTimeFormats lists date formats Strike may use, tried in order.
var strikeTimeFormats = []string{
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	time.RFC3339,
	"2006-01-02 15:04:05",
	"01/02/2006 15:04:05",
	"01/02/2006",
	"2006-01-02",
}

func parseStrikeRow(record []string) (StrikeRow, error) {
	txID := strings.TrimSpace(record[0])
	dateStr := strings.TrimSpace(record[1])
	typ := strings.TrimSpace(record[2])
	btcStr := strings.TrimSpace(record[3])
	usdStr := strings.TrimSpace(record[4])
	feeStr := strings.TrimSpace(record[5])
	status := strings.TrimSpace(record[6])

	// Parse date
	var date time.Time
	var parseErr error
	for _, format := range strikeTimeFormats {
		date, parseErr = time.Parse(format, dateStr)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return StrikeRow{}, fmt.Errorf("invalid date %q", dateStr)
	}

	// Parse BTC amount
	amountBTC, err := strconv.ParseFloat(btcStr, 64)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid BTC amount %q", btcStr)
	}
	amountSat := int64(math.Round(amountBTC * 1e8))

	// Parse USD amount
	amountUSD, err := parseUSD(usdStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid USD amount %q", usdStr)
	}

	// Parse fee
	feeUSD, err := parseUSD(feeStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid fee %q", feeStr)
	}

	return StrikeRow{
		TransactionID: txID,
		Date:          date,
		Type:          typ,
		AmountSat:     amountSat,
		AmountBTC:     amountBTC,
		AmountUSD:     amountUSD,
		FeeUSD:        feeUSD,
		Status:        status,
	}, nil
}

// parseUSD parses a USD string, stripping optional $ and commas.
func parseUSD(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, nil
	}
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")
	return strconv.ParseFloat(s, 64)
}
