// Strike REST API client.
// Docs: https://docs.strike.me/
// Base URL: https://api.strike.me/v1
package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

const strikeDefaultBaseURL = "https://api.strike.me/v1"

// StrikeAPIClient fetches transaction rows from the Strike REST API.
// Rows are returned as StrikeRow values, fully compatible with ImportStrikeCSV
// for deduplication against CSV-imported data.
type StrikeAPIClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// StrikeAPIOption configures a StrikeAPIClient.
type StrikeAPIOption func(*StrikeAPIClient)

// WithStrikeAPIBaseURL overrides the base URL. Used in tests to point at a mock server.
func WithStrikeAPIBaseURL(url string) StrikeAPIOption {
	return func(c *StrikeAPIClient) { c.baseURL = url }
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

type strikeInvoiceListResp struct {
	Items []strikeInvoiceItem `json:"items"`
}

type strikeInvoiceItem struct {
	InvoiceID   string        `json:"invoiceId"`
	Amount      strikeAmount  `json:"amount"`
	State       string        `json:"state"`
	Created     time.Time     `json:"created"`
	Description string        `json:"description"`
}

type strikeAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type strikeBalanceItem struct {
	Currency  string `json:"currency"`
	Total     string `json:"total"`
	Available string `json:"available"`
}

// FetchRows fetches completed transactions from the Strike API and returns them as
// StrikeRow values. Currently covers paid invoices (type "receive"). The rows share
// the same TransactionID format as Strike's CSV export, so ImportStrikeCSV will
// deduplicate correctly when both sources are used.
func (c *StrikeAPIClient) FetchRows(ctx context.Context) ([]StrikeRow, error) {
	url := c.baseURL + "/invoices?$filter=state%20eq%20'PAID'&$top=500&$orderby=created%20desc"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("strike api: invalid API key (401)")
	default:
		return nil, fmt.Errorf("strike api: unexpected status %d", resp.StatusCode)
	}

	var list strikeInvoiceListResp
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("strike api: decode response: %w", err)
	}

	rows := make([]StrikeRow, 0, len(list.Items))
	for _, item := range list.Items {
		row, err := strikeInvoiceToRow(item)
		if err != nil {
			continue // skip non-BTC or malformed entries
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// FetchBalance returns the current BTC balance in satoshis.
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

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("strike api: balance status %d", resp.StatusCode)
	}

	var balances []strikeBalanceItem
	if err := json.NewDecoder(resp.Body).Decode(&balances); err != nil {
		return 0, fmt.Errorf("strike api: decode balances: %w", err)
	}

	for _, b := range balances {
		if b.Currency == "BTC" {
			btc, err := strconv.ParseFloat(b.Total, 64)
			if err != nil {
				return 0, fmt.Errorf("strike api: parse BTC balance %q: %w", b.Total, err)
			}
			return int64(math.Round(btc * 1e8)), nil
		}
	}
	return 0, nil
}

// strikeInvoiceToRow converts a paid invoice from the API into a StrikeRow.
// The InvoiceID maps to the "Reference" column in Strike CSV exports, ensuring
// deduplication works when both import methods are used.
func strikeInvoiceToRow(item strikeInvoiceItem) (StrikeRow, error) {
	if item.Amount.Currency != "BTC" {
		return StrikeRow{}, fmt.Errorf("non-BTC invoice currency %q", item.Amount.Currency)
	}
	btc, err := strconv.ParseFloat(item.Amount.Amount, 64)
	if err != nil {
		return StrikeRow{}, fmt.Errorf("parse BTC amount %q: %w", item.Amount.Amount, err)
	}
	amtSat := int64(math.Round(btc * 1e8))
	return StrikeRow{
		TransactionID: item.InvoiceID,
		Date:          item.Created,
		Type:          "receive",
		AmountSat:     amtSat,
		AmountBTC:     btc,
		Description:   item.Description,
		Status:        "completed",
	}, nil
}
