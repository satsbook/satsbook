package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/apikey"
	"github.com/satsbook/satsbook/internal/db"
)

// --- mock API key store ---

type mockAPIKeyStore struct {
	keys    []db.APIKey
	lookupFn func(hash string) *db.APIKey
}

func (m *mockAPIKeyStore) LookupAPIKey(_ context.Context, hash string) (*db.APIKey, error) {
	if m.lookupFn != nil {
		return m.lookupFn(hash), nil
	}
	for i := range m.keys {
		if m.keys[i].KeyHash == hash {
			return &m.keys[i], nil
		}
	}
	return nil, nil
}
func (m *mockAPIKeyStore) TouchAPIKeyLastUsed(_ context.Context, _ int64) error { return nil }
func (m *mockAPIKeyStore) CreateAPIKey(_ context.Context, name, hash, prefix string) (int64, error) {
	k := db.APIKey{ID: int64(len(m.keys) + 1), Name: name, KeyHash: hash, KeyPrefix: prefix, CreatedAt: time.Now()}
	m.keys = append(m.keys, k)
	return k.ID, nil
}
func (m *mockAPIKeyStore) ListAPIKeys(_ context.Context) ([]db.APIKey, error) { return m.keys, nil }
func (m *mockAPIKeyStore) RevokeAPIKey(_ context.Context, id int64) error {
	for i := range m.keys {
		if m.keys[i].ID == id {
			m.keys[i].Revoked = true
		}
	}
	return nil
}

// --- mock v1 store ---

type mockAPIv1Store struct {
	forwardingFn    func(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error)
	channelStatsFn  func(ctx context.Context) ([]db.ChannelStat, error)
	feeSummaryFn    func(ctx context.Context, since time.Time) (int64, int64, error)
	activeChFn      func(ctx context.Context) (int, error)
	walletBalanceFn func(ctx context.Context) (*db.WalletBalanceSnapshot, error)
	txsFn           func(ctx context.Context, f db.TransactionFilter) (*db.UnifiedTransactionPage, error)
	lotsFn          func(ctx context.Context) ([]db.BTCLot, error)
}

func (m *mockAPIv1Store) ForwardingEvents(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error) {
	if m.forwardingFn != nil {
		return m.forwardingFn(ctx, from, to, limit, offset)
	}
	return &db.ForwardingPage{}, nil
}
func (m *mockAPIv1Store) ChannelStats(ctx context.Context) ([]db.ChannelStat, error) {
	if m.channelStatsFn != nil {
		return m.channelStatsFn(ctx)
	}
	return nil, nil
}
func (m *mockAPIv1Store) FeeSummary(ctx context.Context, since time.Time) (int64, int64, error) {
	if m.feeSummaryFn != nil {
		return m.feeSummaryFn(ctx, since)
	}
	return 0, 0, nil
}
func (m *mockAPIv1Store) ActiveChannelCount(ctx context.Context) (int, error) {
	if m.activeChFn != nil {
		return m.activeChFn(ctx)
	}
	return 0, nil
}
func (m *mockAPIv1Store) LatestWalletBalance(ctx context.Context) (*db.WalletBalanceSnapshot, error) {
	if m.walletBalanceFn != nil {
		return m.walletBalanceFn(ctx)
	}
	return nil, nil
}
func (m *mockAPIv1Store) ListUnifiedTransactions(ctx context.Context, f db.TransactionFilter) (*db.UnifiedTransactionPage, error) {
	if m.txsFn != nil {
		return m.txsFn(ctx, f)
	}
	return &db.UnifiedTransactionPage{}, nil
}
func (m *mockAPIv1Store) ListBTCLots(ctx context.Context) ([]db.BTCLot, error) {
	if m.lotsFn != nil {
		return m.lotsFn(ctx)
	}
	return nil, nil
}

// newV1Handler creates a Handler wired for v1 API tests with a valid key pre-seeded.
func newV1Handler(t *testing.T) (*Handler, string) {
	t.Helper()
	raw, hash, prefix, err := apikey.Generate()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyStore := &mockAPIKeyStore{
		keys: []db.APIKey{
			{ID: 1, Name: "test", KeyHash: hash, KeyPrefix: prefix, CreatedAt: time.Now()},
		},
	}
	h := newTestHandler(nil, nil, nil)
	h.apiKeyStore = keyStore
	h.apiv1Store = &mockAPIv1Store{}
	return h, raw
}

func bearerReq(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// --- auth middleware tests ---

func TestAPIv1Auth_MissingToken(t *testing.T) {
	h := newTestHandler(nil, nil, nil)
	h.apiKeyStore = &mockAPIKeyStore{}
	h.apiv1Store = &mockAPIv1Store{}

	handler := h.apiV1Auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAPIv1Auth_InvalidToken(t *testing.T) {
	h := newTestHandler(nil, nil, nil)
	h.apiKeyStore = &mockAPIKeyStore{} // empty — no keys
	h.apiv1Store = &mockAPIv1Store{}

	handler := h.apiV1Auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	handler(w, bearerReq(t, http.MethodGet, "/api/v1/summary", "sbk_badtoken"))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAPIv1Auth_ValidToken(t *testing.T) {
	h, raw := newV1Handler(t)

	handler := h.apiV1Auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	handler(w, bearerReq(t, http.MethodGet, "/api/v1/summary", raw))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIv1Auth_RateLimit(t *testing.T) {
	h, raw := newV1Handler(t)

	handler := h.apiV1Auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Exhaust the rate limit.
	for i := 0; i < apikey.RateLimit; i++ {
		w := httptest.NewRecorder()
		handler(w, bearerReq(t, http.MethodGet, "/api/v1/summary", raw))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// Next request should be rate-limited.
	w := httptest.NewRecorder()
	handler(w, bearerReq(t, http.MethodGet, "/api/v1/summary", raw))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

// --- endpoint tests ---

func TestHandleV1Summary(t *testing.T) {
	h, raw := newV1Handler(t)
	h.apiv1Store = &mockAPIv1Store{
		feeSummaryFn: func(_ context.Context, since time.Time) (int64, int64, error) {
			return 5_000_000, 42, nil
		},
		activeChFn: func(_ context.Context) (int, error) { return 10, nil },
	}

	w := httptest.NewRecorder()
	h.apiV1Auth(h.HandleV1Summary)(w, bearerReq(t, http.MethodGet, "/api/v1/summary", raw))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["active_channels"] == nil {
		t.Error("expected active_channels in response")
	}
}

func TestHandleV1Forwarding(t *testing.T) {
	h, raw := newV1Handler(t)
	h.apiv1Store = &mockAPIv1Store{
		forwardingFn: func(_ context.Context, _, _ time.Time, _, _ int) (*db.ForwardingPage, error) {
			return &db.ForwardingPage{
				Events: []db.ForwardingEvent{{FeeMsat: 1000}},
				Total:  1,
			}, nil
		},
	}

	w := httptest.NewRecorder()
	h.apiV1Auth(h.HandleV1Forwarding)(w, bearerReq(t, http.MethodGet, "/api/v1/forwarding", raw))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, _ := resp["data"].([]any)
	if len(data) != 1 {
		t.Errorf("expected 1 event, got %d", len(data))
	}
}

func TestHandleV1Channels(t *testing.T) {
	h, raw := newV1Handler(t)
	h.apiv1Store = &mockAPIv1Store{
		channelStatsFn: func(_ context.Context) ([]db.ChannelStat, error) {
			return []db.ChannelStat{{ChanID: 123}}, nil
		},
	}

	w := httptest.NewRecorder()
	h.apiV1Auth(h.HandleV1Channels)(w, bearerReq(t, http.MethodGet, "/api/v1/channels", raw))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleV1Transactions(t *testing.T) {
	h, raw := newV1Handler(t)
	h.apiv1Store = &mockAPIv1Store{
		txsFn: func(_ context.Context, _ db.TransactionFilter) (*db.UnifiedTransactionPage, error) {
			return &db.UnifiedTransactionPage{Total: 0}, nil
		},
	}

	w := httptest.NewRecorder()
	h.apiV1Auth(h.HandleV1Transactions)(w, bearerReq(t, http.MethodGet, "/api/v1/transactions", raw))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleV1Score(t *testing.T) {
	h, raw := newV1Handler(t)
	h.apiv1Store = &mockAPIv1Store{
		feeSummaryFn: func(_ context.Context, _ time.Time) (int64, int64, error) {
			return 7_000_000, 70, nil
		},
		activeChFn: func(_ context.Context) (int, error) { return 5, nil },
	}

	w := httptest.NewRecorder()
	h.apiV1Auth(h.HandleV1Score)(w, bearerReq(t, http.MethodGet, "/api/v1/score", raw))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["score"] == nil {
		t.Error("expected score field")
	}
}

func TestHandleV1MethodNotAllowed(t *testing.T) {
	h, raw := newV1Handler(t)

	endpoints := []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/v1/summary", h.HandleV1Summary},
		{"/api/v1/forwarding", h.HandleV1Forwarding},
		{"/api/v1/channels", h.HandleV1Channels},
		{"/api/v1/transactions", h.HandleV1Transactions},
		{"/api/v1/score", h.HandleV1Score},
	}

	for _, ep := range endpoints {
		w := httptest.NewRecorder()
		h.apiV1Auth(ep.handler)(w, bearerReq(t, http.MethodPost, ep.path, raw))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", ep.path, w.Code)
		}
	}
}

// --- helper tests ---

func TestV1ParseTime(t *testing.T) {
	def := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := v1ParseTime("", def); !got.Equal(def) {
		t.Errorf("empty string should return default")
	}
	if got := v1ParseTime("2024-06-15", def); got.Year() != 2024 {
		t.Errorf("date-only parse: got year %d", got.Year())
	}
	if got := v1ParseTime("2024-06-15T10:00:00Z", def); got.Hour() != 10 {
		t.Errorf("RFC3339 parse: got hour %d", got.Hour())
	}
	if got := v1ParseTime("not-a-date", def); !got.Equal(def) {
		t.Errorf("invalid string should return default")
	}
}

func TestClampInt(t *testing.T) {
	if clampInt(5, 1, 10) != 5 {
		t.Error("in range")
	}
	if clampInt(0, 1, 10) != 1 {
		t.Error("below min")
	}
	if clampInt(100, 1, 10) != 10 {
		t.Error("above max")
	}
}
