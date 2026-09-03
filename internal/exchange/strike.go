// Strike CSV format (as of 2026-03)
// Expected header: Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,
//   Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note
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
	TransactionID   string
	Date            time.Time
	Type            string
	AmountSat       int64
	AmountBTC       float64
	AmountUSD       float64
	FeeUSD          float64
	FeeBTC          float64
	BTCPrice        float64
	CostBasisUSD    float64
	Destination     string
	Description     string
	TransactionHash string
	Status          string // always "completed" for rows present in export
	RawLine         string
}

// StrikeResult holds the result of parsing a Strike CSV.
type StrikeResult struct {
	Rows   []StrikeRow
	Errors []string
}

// IsPurchase returns true if the row represents a BTC purchase.
func (r StrikeRow) IsPurchase() bool {
	t := strings.ToLower(r.Type)
	return t == "purchase" || t == "buy"
}

// IsReceive returns true if the row represents an incoming BTC receive.
func (r StrikeRow) IsReceive() bool {
	return strings.EqualFold(r.Type, "receive")
}

// IsSale returns true if the row represents a BTC sale.
func (r StrikeRow) IsSale() bool {
	return strings.EqualFold(r.Type, "sale")
}

// IsIgnored returns true if the row has no BTC impact and should be skipped.
func (r StrikeRow) IsIgnored() bool {
	return r.Classify() == ClassIgnored
}

// StrikeClassification represents the classification of a Strike CSV row.
type StrikeClassification string

const (
	ClassAcquisition StrikeClassification = "acquisition"
	ClassDisposal    StrikeClassification = "disposal"
	ClassIgnored     StrikeClassification = "ignored"
)

// Classify returns the classification for the row.
//
// For explicitly-typed rows (Purchase, Buy, Receive, Sale) the type name drives
// the classification. For all other types (Send, Loan collateral, Reversal, etc.)
// we fall back to the sign of AmountBTC so that new Strike transaction types are
// automatically captured for portfolio balance tracking. Rows with zero BTC
// (Deposit, Withdrawal, Line of credit draw, Principal payment, etc.) are ignored
// since they carry no BTC impact.
func (r StrikeRow) Classify() StrikeClassification {
	if r.IsPurchase() || r.IsReceive() {
		return ClassAcquisition
	}
	if r.IsSale() {
		return ClassDisposal
	}
	// Fall back to amount direction for unrecognized types.
	if r.AmountBTC > 0 {
		return ClassAcquisition
	}
	if r.AmountBTC < 0 {
		return ClassDisposal
	}
	return ClassIgnored
}

var expectedStrikeHeader = []string{
	"Reference", "Date & Time (UTC)", "Transaction Type",
	"Amount USD", "Fee USD", "Amount BTC", "Fee BTC",
	"BTC Price", "Cost Basis (USD)", "Destination",
	"Description", "Transaction Hash", "Note",
}

const strikeMinColumns = 13

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

		if len(record) < strikeMinColumns {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected %d columns, got %d", rowNum, strikeMinColumns, len(record)))
			continue
		}

		// Skip rows with empty reference
		txID := strings.TrimSpace(record[0])
		if txID == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: empty reference", rowNum))
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
	if len(header) < strikeMinColumns {
		return fmt.Errorf("invalid Strike CSV format: expected %d columns, got %d", strikeMinColumns, len(header))
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
	"Jan 02 2006 15:04:05",  // Strike's actual format
	"Jan 2 2006 15:04:05",   // single-digit day
	"2006-01-02T15:04:05Z",  // ISO 8601
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
	amtUSDStr := strings.TrimSpace(record[3])
	feeUSDStr := strings.TrimSpace(record[4])
	amtBTCStr := strings.TrimSpace(record[5])
	feeBTCStr := strings.TrimSpace(record[6])
	btcPriceStr := strings.TrimSpace(record[7])
	costBasisStr := strings.TrimSpace(record[8])
	destination := strings.TrimSpace(record[9])
	description := strings.TrimSpace(record[10])
	txHash := strings.TrimSpace(record[11])

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

	// Parse BTC amount (can be empty or negative)
	amountBTC, err := parseOptionalFloat(amtBTCStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid BTC amount %q", amtBTCStr)
	}
	amountSat := int64(math.Round(amountBTC * 1e8))

	// Parse USD amount (can be empty or negative)
	amountUSD, err := parseUSD(amtUSDStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid USD amount %q", amtUSDStr)
	}

	// Parse fee USD
	feeUSD, err := parseUSD(feeUSDStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid fee USD %q", feeUSDStr)
	}

	// Parse fee BTC (optional)
	feeBTC, err := parseOptionalFloat(feeBTCStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid fee BTC %q", feeBTCStr)
	}

	// Parse BTC price (optional)
	btcPrice, err := parseOptionalFloat(btcPriceStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid BTC price %q", btcPriceStr)
	}

	// Parse cost basis (optional)
	costBasis, err := parseUSD(costBasisStr)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("invalid cost basis %q", costBasisStr)
	}

	return StrikeRow{
		TransactionID:   txID,
		Date:            date,
		Type:            typ,
		AmountSat:       amountSat,
		AmountBTC:       amountBTC,
		AmountUSD:       amountUSD,
		FeeUSD:          feeUSD,
		FeeBTC:          feeBTC,
		BTCPrice:        btcPrice,
		CostBasisUSD:    costBasis,
		Destination:     destination,
		Description:     description,
		TransactionHash: txHash,
		Status:          "completed", // all rows in export are completed
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

// parseOptionalFloat parses a float that may be empty.
func parseOptionalFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// ParseStrikeRawLine parses a single raw CSV data line (no header) into a StrikeRow.
// The line must be a comma-joined record as produced by RawLine on a parsed row.
func ParseStrikeRawLine(line string) (StrikeRow, error) {
	r := csv.NewReader(strings.NewReader(line))
	record, err := r.Read()
	if err != nil {
		return StrikeRow{}, fmt.Errorf("parse raw line: %w", err)
	}
	if len(record) < strikeMinColumns {
		return StrikeRow{}, fmt.Errorf("parse raw line: expected %d columns, got %d", strikeMinColumns, len(record))
	}
	row, err := parseStrikeRow(record)
	if err != nil {
		return StrikeRow{}, err
	}
	row.RawLine = line
	return row, nil
}
