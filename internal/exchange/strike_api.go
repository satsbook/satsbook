// Strike REST API client.
//
// # Scope of the Strike API
//
// The Strike API is a payment-processing API aimed at merchants and developers,
// not a personal transaction-history API.  The endpoints that exist do NOT cover
// the same transaction types as Strike's own CSV export.
//
// What IS available via API:
//   - GET /v1/invoices       — Lightning payment invoices you created that others paid
//                              (scope: partner.invoice.read)
//   - GET /v1/balances       — Live multi-currency account balances
//                              (scope: partner.balances.read)
//   - GET /v1/deposits       — Bank deposits (fiat into Strike), NOT BTC purchases
//   - GET /v1/payouts        — Bank payouts (fiat out of Strike), NOT BTC withdrawals
//
// What is NOT available via API:
//   - BTC purchases / DCA orders
//   - BTC sales
//   - BTC withdrawals to external wallets
//   - Consumer app transaction history
//
// For the above, CSV import remains the only option.
//
// Deduplication note: the invoiceId returned by GET /v1/invoices is used as
// TransactionID in StrikeRow, which maps to the "Reference" column in Strike's
// CSV export.  Dedup via ImportStrikeCSV works when the same invoice appears in
// both sources, but CSV-only transactions (purchases, withdrawals, sales) will
// never appear from the API.
package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const strikeDefaultBaseURL = "https://api.strike.me/v1"

// strikePageSize is the maximum number of records Strike returns per page.
// The API enforces a hard cap of 100 on $top.
const strikePageSize = 100

// StrikeAPIClient fetches data from the Strike REST API.
// It covers invoice-type receives and live balances only; see package comment
// for what is NOT accessible via the API.
type StrikeAPIClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// StrikeAPIOption configures a StrikeAPIClient.
type StrikeAPIOption func(*StrikeAPIClient)

// WithStrikeAPIBaseURL overrides the base URL.  Used in tests to point at a mock server.
func WithStrikeAPIBaseURL(u string) StrikeAPIOption {
	return func(c *StrikeAPIClient) { c.baseURL = u }
}

// NewStrikeAPIClient creates a Strike API client authenticated with apiKey.
func NewStrikeAPIClient(apiKey string, opts ...StrikeAPIOption) *StrikeAPIClient {
	c := &StrikeAPIClient{
		apiKey:  apiKey,
		baseURL: strikeDefaultBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- internal response types ---

type strikePagedResponse struct {
	Items          json.RawMessage `json:"items"`
	Count          int64           `json:"count"`
	IsCountUnknown bool            `json:"isCountUnknown"`
}

type strikeInvoiceItem struct {
	InvoiceID    string                     `json:"invoiceId"`
	Amount       strikeMonetaryAmount       `json:"amount"`
	State        string                     `json:"state"`
	Created      time.Time                  `json:"created"`
	Description  string                     `json:"description"`
	Transactions []strikeInvoiceTransaction `json:"transactions"`
}

type strikeInvoiceTransaction struct {
	TransactionID  string               `json:"transactionId"`
	State          string               `json:"state"`
	AmountReceived strikeMonetaryAmount `json:"amountReceived"`
	Created        time.Time            `json:"created"`
	Completed      *time.Time           `json:"completed"`
}

type strikeMonetaryAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type strikeBalanceItem struct {
	Currency  string `json:"currency"`
	Current   string `json:"current"`
	Pending   string `json:"pending"`
	Outgoing  string `json:"outgoing"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
	Total     string `json:"total"`
}

// FetchRows fetches paid invoices from the Strike API and returns them as StrikeRow
// values compatible with ImportStrikeCSV.
//
// IMPORTANT SCOPE LIMITATION: This only returns Lightning payment invoices that were
// created via the Strike API/dashboard and subsequently paid.  It does NOT include
// BTC purchases (DCA), sales, or withdrawals — those transaction types have no
// equivalent API endpoint and must still be imported via CSV.
//
// Required API scope: partner.invoice.read
func (c *StrikeAPIClient) FetchRows(ctx context.Context) ([]StrikeRow, error) {
	var all []StrikeRow
	skip := 0

	for {
		q := url.Values{}
		q.Set("$filter", "state eq 'PAID'")
		q.Set("$orderby", "created desc")
		q.Set("$top", strconv.Itoa(strikePageSize))
		q.Set("$skip", strconv.Itoa(skip))
		q.Set("includeTransactions", "true")

		endpoint := c.baseURL + "/invoices?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("strike api: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("strike api: request: %w", err)
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			// continue
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("strike api: authentication failed (401) — check API key and partner.invoice.read scope")
		case http.StatusForbidden:
			return nil, fmt.Errorf("strike api: forbidden (403) — API key is missing partner.invoice.read scope")
		default:
			return nil, fmt.Errorf("strike api: unexpected status %d", resp.StatusCode)
		}

		var page strikePagedResponse
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, fmt.Errorf("strike api: decode response: %w", err)
		}

		var items []strikeInvoiceItem
		if err := json.Unmarshal(page.Items, &items); err != nil {
			return nil, fmt.Errorf("strike api: decode items: %w", err)
		}

		for _, item := range items {
			row, err := strikeInvoiceToRow(item)
			if err != nil {
				continue // skip non-BTC or malformed entries
			}
			all = append(all, row)
		}

		// Stop when we've received a partial page (no more data).
		if len(items) < strikePageSize {
			break
		}
		skip += strikePageSize
	}

	return all, nil
}

// FetchBalance returns the current BTC balance from Strike in satoshis.
// Uses the available field (spendable balance) rather than total (which includes
// outgoing funds not yet settled).
//
// Required API scope: partner.balances.read
func (c *StrikeAPIClient) FetchBalance(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/balances", nil)
	if err != nil {
		return 0, fmt.Errorf("strike api: build balance request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("strike api: balance request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusUnauthorized:
		return 0, fmt.Errorf("strike api: authentication failed (401) — check API key and partner.balances.read scope")
	case http.StatusForbidden:
		return 0, fmt.Errorf("strike api: forbidden (403) — API key is missing partner.balances.read scope")
	default:
		return 0, fmt.Errorf("strike api: balance status %d", resp.StatusCode)
	}

	var balances []strikeBalanceItem
	if err := json.NewDecoder(resp.Body).Decode(&balances); err != nil {
		return 0, fmt.Errorf("strike api: decode balances: %w", err)
	}

	for _, b := range balances {
		if b.Currency != "BTC" {
			continue
		}
		// Use Available (spendable) rather than Total (available + outgoing).
		btc, err := strconv.ParseFloat(b.Available, 64)
		if err != nil {
			return 0, fmt.Errorf("strike api: parse BTC available balance %q: %w", b.Available, err)
		}
		return int64(math.Round(btc * 1e8)), nil
	}
	return 0, nil
}

// strikeInvoiceToRow converts a paid Strike invoice to a StrikeRow.
//
// The InvoiceID is used as TransactionID (maps to "Reference" in Strike CSV).
// Only BTC-denominated invoices are converted; USD/fiat invoices are skipped.
func strikeInvoiceToRow(item strikeInvoiceItem) (StrikeRow, error) {
	// Determine BTC amount: prefer actual received amount from the transaction
	// sub-object (most accurate), fall back to the invoice face amount.
	var btcAmount float64
	var txID string
	var txDate time.Time

	if len(item.Transactions) > 0 {
		// Use the first completed transaction's received amount.
		for _, tx := range item.Transactions {
			if tx.State == "COMPLETED" && tx.AmountReceived.Currency == "BTC" {
				received, err := strconv.ParseFloat(tx.AmountReceived.Amount, 64)
				if err == nil {
					btcAmount = received
					txID = tx.TransactionID
					if tx.Completed != nil {
						txDate = *tx.Completed
					} else {
						txDate = tx.Created
					}
				}
				break
			}
		}
	}

	// Fall back to invoice-level amount if no transaction data available.
	if btcAmount == 0 {
		if item.Amount.Currency != "BTC" {
			return StrikeRow{}, fmt.Errorf("non-BTC invoice currency %q", item.Amount.Currency)
		}
		faceAmt, err := strconv.ParseFloat(item.Amount.Amount, 64)
		if err != nil {
			return StrikeRow{}, fmt.Errorf("parse invoice amount %q: %w", item.Amount.Amount, err)
		}
		btcAmount = faceAmt
		txDate = item.Created
	}

	// Use transaction ID when available for better dedup; fall back to invoice ID.
	if txID == "" {
		txID = item.InvoiceID
	}
	if txDate.IsZero() {
		txDate = item.Created
	}

	amtSat := int64(math.Round(btcAmount * 1e8))
	return StrikeRow{
		TransactionID: txID,
		Date:          txDate,
		Type:          "receive",
		AmountSat:     amtSat,
		AmountBTC:     btcAmount,
		Description:   item.Description,
		Status:        "completed",
	}, nil
}
