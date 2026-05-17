package license

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// LicenseStore is the DB interface consumed by the checker.
type LicenseStore interface {
	GetCachedLicense(ctx context.Context) (*CachedLicense, error)
	UpdateCachedLicense(ctx context.Context, cl *CachedLicense) error
}

// CachedLicense mirrors db.CachedLicense to avoid circular imports.
type CachedLicense struct {
	LicenseKey   string
	Tier         string
	SignedToken  string
	LastVerified time.Time
	ExpiresAt    time.Time
}

// Checker validates license keys and provides the current tier.
type Checker interface {
	CurrentTier() Tier
	Verify(ctx context.Context) error
}

// DefaultChecker implements phone-home license validation with local caching.
type DefaultChecker struct {
	store         LicenseStore
	licenseKey    string
	validationURL string
	gracePeriod   time.Duration
	httpClient    *http.Client
	logger        *log.Logger
	publicKey     ed25519.PublicKey
	currentTier   atomic.Value // stores Tier
	nowFunc       func() time.Time
}

// Option configures a DefaultChecker.
type Option func(*DefaultChecker)

// WithValidationURL overrides the default validation endpoint.
func WithValidationURL(url string) Option {
	return func(c *DefaultChecker) { c.validationURL = url }
}

// WithGracePeriod overrides the default 7-day grace period.
func WithGracePeriod(d time.Duration) Option {
	return func(c *DefaultChecker) { c.gracePeriod = d }
}

// WithHTTPClient overrides the default HTTP client (useful for testing).
func WithHTTPClient(client *http.Client) Option {
	return func(c *DefaultChecker) { c.httpClient = client }
}

// WithLogger sets the logger for the checker.
func WithLogger(l *log.Logger) Option {
	return func(c *DefaultChecker) { c.logger = l }
}

// WithPublicKey overrides the embedded public key (for testing).
func WithPublicKey(key ed25519.PublicKey) Option {
	return func(c *DefaultChecker) { c.publicKey = key }
}

// withNowFunc overrides time.Now for testing.
func withNowFunc(fn func() time.Time) Option {
	return func(c *DefaultChecker) { c.nowFunc = fn }
}

const defaultValidationURL = "https://api.satsbook.io/v1/license/validate"

// NewChecker creates a license checker with the given options.
func NewChecker(store LicenseStore, licenseKey string, opts ...Option) *DefaultChecker {
	c := &DefaultChecker{
		store:         store,
		licenseKey:    licenseKey,
		validationURL: defaultValidationURL,
		gracePeriod:   7 * 24 * time.Hour,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		logger:        log.New(log.Writer(), "[license] ", log.LstdFlags),
		publicKey:     PublicKey(),
		nowFunc:       time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	c.currentTier.Store(TierFree)
	return c
}

// CurrentTier returns the cached tier. Safe for concurrent use.
func (c *DefaultChecker) CurrentTier() Tier {
	return c.currentTier.Load().(Tier)
}

func (c *DefaultChecker) setTier(t Tier) {
	c.currentTier.Store(t)
}

// Verify performs a phone-home check and updates the local cache.
// On network failure, it falls back to the cached result within the grace period.
func (c *DefaultChecker) Verify(ctx context.Context) error {
	// No license key = free tier, no network call needed.
	if c.licenseKey == "" {
		c.setTier(TierFree)
		return c.store.UpdateCachedLicense(ctx, &CachedLicense{Tier: string(TierFree)})
	}

	// Attempt phone-home validation.
	tier, signedToken, err := c.phoneHome(ctx)
	if err == nil {
		// Success: update cache with new grace period and the signed token.
		now := c.nowFunc()
		cl := &CachedLicense{
			LicenseKey:   c.licenseKey,
			Tier:         string(tier),
			SignedToken:  signedToken,
			LastVerified: now,
			ExpiresAt:    now.Add(c.gracePeriod),
		}
		c.setTier(tier)
		if storeErr := c.store.UpdateCachedLicense(ctx, cl); storeErr != nil {
			c.logger.Printf("failed to update license cache: %v", storeErr)
		}
		c.logger.Printf("license verified: tier=%s (signed)", tier)
		return nil
	}

	c.logger.Printf("phone-home failed: %v — checking cache", err)

	// Fallback to cached license within grace period.
	return c.fallbackToCache(ctx)
}

// phoneHome calls the validation API and returns the tier and signed token.
func (c *DefaultChecker) phoneHome(ctx context.Context) (Tier, string, error) {
	body, err := json.Marshal(map[string]string{"key": c.licenseKey})
	if err != nil {
		return TierFree, "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.validationURL, bytes.NewReader(body))
	if err != nil {
		return TierFree, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TierFree, "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return TierFree, "", fmt.Errorf("validation returned status %d", resp.StatusCode)
	}

	var result struct {
		Valid bool   `json:"valid"`
		Tier  string `json:"tier"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TierFree, "", fmt.Errorf("decode response: %w", err)
	}

	if !result.Valid {
		return TierFree, "", nil
	}

	// Verify the signed token from the server.
	if result.Token == "" {
		return TierFree, "", fmt.Errorf("server returned valid=true but no signed token")
	}

	payload, err := VerifyTokenAt(result.Token, c.publicKey, c.nowFunc())
	if err != nil {
		return TierFree, "", fmt.Errorf("invalid token from server: %w", err)
	}

	// Token's key must match our license key.
	if payload.Key != c.licenseKey {
		return TierFree, "", fmt.Errorf("token key mismatch")
	}

	tier := Tier(payload.Tier)
	if !ValidTier(payload.Tier) {
		return TierFree, "", fmt.Errorf("unknown tier %q in token", payload.Tier)
	}

	return tier, result.Token, nil
}

// fallbackToCache reads the cached license, verifies the stored token signature,
// and checks the grace period.
func (c *DefaultChecker) fallbackToCache(ctx context.Context) error {
	cached, err := c.store.GetCachedLicense(ctx)
	if err != nil {
		c.logger.Printf("failed to read license cache: %v", err)
		c.setTier(TierFree)
		return err
	}

	// If the cached key doesn't match the current key, we can't trust it.
	if cached.LicenseKey != c.licenseKey {
		c.logger.Printf("cached key mismatch — defaulting to free")
		c.setTier(TierFree)
		return nil
	}

	// Verify the cached signed token. If there's no token or it has an
	// invalid signature, don't trust the cached tier.
	if cached.SignedToken == "" {
		c.logger.Printf("no signed token in cache — defaulting to free")
		c.setTier(TierFree)
		return nil
	}

	// Use the grace period expiry for time check, not the token's own expiry,
	// since the token expiry may be shorter than the grace period.
	// We still verify the signature is valid.
	payload, err := VerifyTokenAt(cached.SignedToken, c.publicKey, c.nowFunc())
	if err != nil {
		// Token signature invalid or token itself expired — check grace period.
		// If grace period is still active, we accept the cached tier from the DB
		// but only if the signature at least verifies (just expired).
		// For simplicity: if signature is bad, downgrade immediately.
		c.logger.Printf("cached token verification failed: %v — downgrading to free", err)
		c.setTier(TierFree)
		return nil
	}

	// Signature valid, token not expired — trust it.
	now := c.nowFunc()
	if now.Before(cached.ExpiresAt) {
		tier := Tier(payload.Tier)
		c.setTier(tier)
		c.logger.Printf("grace period active: tier=%s (signed, expires %s)", tier, cached.ExpiresAt.Format(time.RFC3339))
		return nil
	}

	// Grace period expired — downgrade to free.
	c.logger.Printf("grace period expired — downgrading to free")
	c.setTier(TierFree)
	if storeErr := c.store.UpdateCachedLicense(ctx, &CachedLicense{
		LicenseKey: c.licenseKey,
		Tier:       string(TierFree),
	}); storeErr != nil {
		c.logger.Printf("failed to update cache on downgrade: %v", storeErr)
	}
	return nil
}

// FreeChecker always returns TierFree. Used when no license system is configured.
type FreeChecker struct{}

func (FreeChecker) CurrentTier() Tier              { return TierFree }
func (FreeChecker) Verify(_ context.Context) error { return nil }
