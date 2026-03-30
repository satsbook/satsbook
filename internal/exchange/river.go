// River CSV format (as of 2026-03)
// Expected header: Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag
package exchange

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// RiverRow represents a single parsed row from a River CSV export.
// JSON field names match StrikeRow so ExchangeSummary/ExchangeBalance queries work unchanged.
type RiverRow struct {
	Date         time.Time `json:"Date"`
	Type         string    `json:"Type"`         // "buy", "send"
	AmountBTC    float64   `json:"AmountBTC"`    // positive for buys, negative for sends
	AmountSat    int64     `json:"-"`            // derived from AmountBTC
	AmountUSD    float64   `json:"AmountUSD"`    // USD involved (cost for buys, 0 for sends)
	FeeUSD       float64   `json:"FeeUSD"`       // fee in USD (0 when fee is in BTC)
	FeeBTC       float64   `json:"FeeBTC"`       // fee in BTC (0 when fee is in USD)
	CostBasisUSD float64   `json:"CostBasisUSD"` // for buys: total USD spent (sent amount)
	RawLine      string    `json:"-"`
}

// RiverResult holds the result of parsing a River CSV.
type RiverResult struct {
	Rows   []RiverRow
	Errors []string
}

// IsPurchase returns true if the row represents a BTC purchase.
func (r RiverRow) IsPurchase() bool {
	return r.Type == "buy"
}

var expectedRiverHeader = []string{
	"Date", "Sent Amount", "Sent Currency", "Received Amount",
	"Received Currency", "Fee Amount", "Fee Currency", "Tag",
}

const riverMinColumns = 8

// ParseRiverCSV parses a River Financial transaction export CSV.
// It validates the header strictly and parses each data row.
// Invalid rows are recorded as errors but do not halt parsing.
func ParseRiverCSV(r io.Reader) (*RiverResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	// Read and validate header
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty CSV file")
		}
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	if err := validateRiverHeader(header); err != nil {
		return nil, err
	}

	result := &RiverResult{}

	for rowNum := 2; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		if len(record) < riverMinColumns {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: expected %d columns, got %d", rowNum, riverMinColumns, len(record)))
			continue
		}

		row, err := parseRiverRow(record)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			continue
		}

		row.RawLine = strings.Join(record, ",")
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

func validateRiverHeader(header []string) error {
	if len(header) < riverMinColumns {
		return fmt.Errorf("invalid River CSV format: expected %d columns, got %d", riverMinColumns, len(header))
	}
	for i, expected := range expectedRiverHeader {
		got := strings.TrimSpace(header[i])
		if !strings.EqualFold(got, expected) {
			return fmt.Errorf("invalid River CSV format: expected column %d to be %q, got %q", i+1, expected, got)
		}
	}
	return nil
}

// riverTimeFormats lists date formats River may use, tried in order.
var riverTimeFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	time.RFC3339,
	"01/02/2006 15:04:05",
	"2006-01-02",
}

func parseRiverRow(record []string) (RiverRow, error) {
	dateStr := strings.TrimSpace(record[0])
	sentAmtStr := strings.TrimSpace(record[1])
	sentCurrency := strings.TrimSpace(record[2])
	recvAmtStr := strings.TrimSpace(record[3])
	recvCurrency := strings.TrimSpace(record[4])
	feeAmtStr := strings.TrimSpace(record[5])
	feeCurrency := strings.TrimSpace(record[6])
	tag := strings.TrimSpace(record[7])

	// Parse date
	var date time.Time
	var parseErr error
	for _, format := range riverTimeFormats {
		date, parseErr = time.Parse(format, dateStr)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return RiverRow{}, fmt.Errorf("invalid date %q", dateStr)
	}

	// Parse amounts
	sentAmt, err := parseOptionalFloat(sentAmtStr)
	if err != nil {
		return RiverRow{}, fmt.Errorf("invalid sent amount %q", sentAmtStr)
	}
	recvAmt, err := parseOptionalFloat(recvAmtStr)
	if err != nil {
		return RiverRow{}, fmt.Errorf("invalid received amount %q", recvAmtStr)
	}
	feeAmt, err := parseOptionalFloat(feeAmtStr)
	if err != nil {
		return RiverRow{}, fmt.Errorf("invalid fee amount %q", feeAmtStr)
	}

	row := RiverRow{Date: date}

	switch strings.ToLower(tag) {
	case "buy":
		// Buy: sent USD, received BTC
		row.Type = "buy"
		row.AmountBTC = recvAmt
		row.AmountUSD = sentAmt
		row.CostBasisUSD = sentAmt
		if strings.EqualFold(feeCurrency, "USD") {
			row.FeeUSD = feeAmt
		} else if strings.EqualFold(feeCurrency, "BTC") {
			row.FeeBTC = feeAmt
		}

	case "withdrawal", "":
		// Withdrawal or unnamed BTC send (empty tag = on-chain send)
		row.Type = "send"
		if strings.EqualFold(sentCurrency, "BTC") {
			// Sent amount is the total BTC leaving the account (may include fee)
			row.AmountBTC = -sentAmt
			if strings.EqualFold(feeCurrency, "BTC") {
				row.FeeBTC = feeAmt
			}
		} else if strings.EqualFold(sentCurrency, "USD") {
			// USD withdrawal — no BTC movement
			row.AmountUSD = sentAmt
			if strings.EqualFold(feeCurrency, "USD") {
				row.FeeUSD = feeAmt
			}
		}

	case "sell":
		row.Type = "sale"
		if strings.EqualFold(sentCurrency, "BTC") {
			row.AmountBTC = -sentAmt
		}
		if strings.EqualFold(recvCurrency, "USD") {
			row.AmountUSD = recvAmt
		}
		if strings.EqualFold(feeCurrency, "USD") {
			row.FeeUSD = feeAmt
		} else if strings.EqualFold(feeCurrency, "BTC") {
			row.FeeBTC = feeAmt
		}

	case "receive":
		row.Type = "receive"
		if strings.EqualFold(recvCurrency, "BTC") {
			row.AmountBTC = recvAmt
		}

	default:
		// Unknown tag — treat as send if BTC is being sent, receive if BTC is received
		if sentAmt > 0 && strings.EqualFold(sentCurrency, "BTC") {
			row.Type = "send"
			row.AmountBTC = -sentAmt
		} else if recvAmt > 0 && strings.EqualFold(recvCurrency, "BTC") {
			row.Type = "receive"
			row.AmountBTC = recvAmt
		} else {
			return RiverRow{}, fmt.Errorf("unknown tag %q with no BTC movement", tag)
		}
	}

	row.AmountSat = int64(math.Round(row.AmountBTC * 1e8))

	return row, nil
}
