package price

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Cache fetches and caches the BTC/USD price with a configurable TTL.
type Cache struct {
	mu        sync.RWMutex
	price     float64
	fetchedAt time.Time
	ttl       time.Duration
	apiURL    string
	client    *http.Client
}

// Option configures the price cache.
type Option func(*Cache)

// WithTTL sets the cache time-to-live.
func WithTTL(d time.Duration) Option {
	return func(c *Cache) { c.ttl = d }
}

// WithAPIURL sets the price API URL.
func WithAPIURL(url string) Option {
	return func(c *Cache) { c.apiURL = url }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Cache) { c.client = client }
}

// NewCache creates a new price cache with the given options.
func NewCache(opts ...Option) *Cache {
	c := &Cache{
		ttl:    5 * time.Minute,
		apiURL: "https://mempool.space/api/v1/prices",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetBTCPrice returns the cached BTC/USD price, fetching if stale.
// If the fetch fails but a stale price exists, the stale price is returned.
func (c *Cache) GetBTCPrice(ctx context.Context) (float64, error) {
	c.mu.RLock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		price := c.price
		c.mu.RUnlock()
		return price, nil
	}
	stalePrice := c.price
	hasStale := !c.fetchedAt.IsZero()
	c.mu.RUnlock()

	// Fetch fresh price
	price, err := c.fetch(ctx)
	if err != nil {
		if hasStale {
			return stalePrice, nil
		}
		return 0, fmt.Errorf("fetch BTC price: %w", err)
	}

	c.mu.Lock()
	c.price = price
	c.fetchedAt = time.Now()
	c.mu.Unlock()

	return price, nil
}

// mempoolPriceResponse is the response from mempool.space /api/v1/prices.
type mempoolPriceResponse struct {
	USD float64 `json:"USD"`
}

func (c *Cache) fetch(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var prices mempoolPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&prices); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	if prices.USD <= 0 {
		return 0, fmt.Errorf("invalid price: %f", prices.USD)
	}

	return prices.USD, nil
}

// FetchedAt returns the time the price was last successfully fetched.
// Returns zero time if never fetched.
func (c *Cache) FetchedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fetchedAt
}

// MsatToUSD converts millisatoshis to USD given a BTC price.
func MsatToUSD(msat int64, btcPrice float64) float64 {
	return (float64(msat) / 100_000_000_000.0) * btcPrice
}
