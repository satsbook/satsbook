package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/monarch"
)

// mockSettingsStore implements SettingsStore for testing.
type mockSettingsStore struct {
	data    map[string]string
	saveErr error
}

func newMockSettingsStore() *mockSettingsStore {
	return &mockSettingsStore{data: make(map[string]string)}
}

func (m *mockSettingsStore) GetSetting(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *mockSettingsStore) SetSetting(_ context.Context, key, value string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.data[key] = value
	return nil
}

func newSettingsHandler(ss SettingsStore) *Handler {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	if ss != nil {
		h.SetSettingsStore(ss)
	}
	return h
}

// --- HandleStrikeAPIKeySave tests (Issue #46: Strike API key management) ---
// The spec says: users can add/remove Strike API key in settings UI.

func TestHandleStrikeAPIKeySave_Success(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{"strike_api_key": {"sk_live_test123"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ss.data["strike_api_key"] != "sk_live_test123" {
		t.Errorf("expected key saved, got %q", ss.data["strike_api_key"])
	}
	if !strings.Contains(w.Body.String(), "saved") {
		t.Errorf("expected success message, got: %s", w.Body.String())
	}
}

func TestHandleStrikeAPIKeySave_CallsCallback(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	var cbKey string
	h.OnStrikeKeyChange(func(key string) { cbKey = key })

	body := url.Values{"strike_api_key": {"sk_test_callback"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)

	if cbKey != "sk_test_callback" {
		t.Errorf("expected callback called with key, got %q", cbKey)
	}
}

func TestHandleStrikeAPIKeySave_EmptyKey(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{"strike_api_key": {""}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)

	// Should return 200 with error HTML fragment, not 400
	// (HTMX pattern: always 200, error shown in target div)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' error in response, got: %s", w.Body.String())
	}
	// Key should not be saved
	if ss.data["strike_api_key"] != "" {
		t.Errorf("empty key should not be saved")
	}
}

func TestHandleStrikeAPIKeySave_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/strike/key", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleStrikeAPIKeySave_NoSettingsStore(t *testing.T) {
	h := newSettingsHandler(nil) // no settings store
	body := url.Values{"strike_api_key": {"sk_test"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when no settings store, got %d", w.Code)
	}
}

func TestHandleStrikeAPIKeySave_SaveError(t *testing.T) {
	ss := &mockSettingsStore{data: make(map[string]string), saveErr: fmt.Errorf("db error")}
	h := newSettingsHandler(ss)

	body := url.Values{"strike_api_key": {"sk_test"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeySave(w, req)

	if !strings.Contains(w.Body.String(), "Failed") || !strings.Contains(w.Body.String(), "alert-error") {
		t.Errorf("expected error alert, got: %s", w.Body.String())
	}
}

// --- HandleStrikeAPIKeyDisconnect tests (Issue #46) ---

func TestHandleStrikeAPIKeyDisconnect_Success(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["strike_api_key"] = "sk_live_test123"
	h := newSettingsHandler(ss)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeyDisconnect(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ss.data["strike_api_key"] != "" {
		t.Errorf("expected key cleared, got %q", ss.data["strike_api_key"])
	}
	if !strings.Contains(w.Body.String(), "removed") {
		t.Errorf("expected removal confirmation, got: %s", w.Body.String())
	}
}

func TestHandleStrikeAPIKeyDisconnect_CallsCallback(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["strike_api_key"] = "existing-key"
	h := newSettingsHandler(ss)

	var cbKey string
	h.OnStrikeKeyChange(func(key string) { cbKey = key })

	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeyDisconnect(w, req)

	// Callback should be called with empty key (disconnect)
	if cbKey != "" {
		t.Errorf("expected callback called with empty key, got %q", cbKey)
	}
}

func TestHandleStrikeAPIKeyDisconnect_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/strike/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeyDisconnect(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleStrikeAPIKeyDisconnect_NoSettingsStore(t *testing.T) {
	h := newSettingsHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeyDisconnect(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when no settings store, got %d", w.Code)
	}
}

// --- HandleCoinbaseAPIKeySave tests (Issue #53: Coinbase API key management) ---
// The spec says: users can add Coinbase API key in settings UI.
// Both Key ID and private key are required.

func TestHandleCoinbaseAPIKeySave_Success(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{
		"coinbase_api_key_id": {"key-uuid-1234"},
		"coinbase_api_secret": {"base64-encoded-private-key"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ss.data["coinbase_api_key_id"] != "key-uuid-1234" {
		t.Errorf("key ID not saved, got %q", ss.data["coinbase_api_key_id"])
	}
	if ss.data["coinbase_api_secret"] != "base64-encoded-private-key" {
		t.Errorf("secret not saved, got %q", ss.data["coinbase_api_secret"])
	}
	if !strings.Contains(w.Body.String(), "saved") {
		t.Errorf("expected success message, got: %s", w.Body.String())
	}
}

func TestHandleCoinbaseAPIKeySave_CallsCallback(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	var cbKeyID, cbSecret string
	h.OnCoinbaseKeyChange(func(keyID, secret string) {
		cbKeyID = keyID
		cbSecret = secret
	})

	body := url.Values{
		"coinbase_api_key_id": {"cb-key-id"},
		"coinbase_api_secret": {"cb-secret"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)

	if cbKeyID != "cb-key-id" || cbSecret != "cb-secret" {
		t.Errorf("callback not called correctly: keyID=%q, secret=%q", cbKeyID, cbSecret)
	}
}

func TestHandleCoinbaseAPIKeySave_MissingKeyID(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{
		"coinbase_api_key_id": {""},
		"coinbase_api_secret": {"some-secret"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' error, got: %s", w.Body.String())
	}
}

func TestHandleCoinbaseAPIKeySave_MissingSecret(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{
		"coinbase_api_key_id": {"key-id"},
		"coinbase_api_secret": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)

	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' error when secret missing, got: %s", w.Body.String())
	}
}

func TestHandleCoinbaseAPIKeySave_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/coinbase/key", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleCoinbaseAPIKeySave_NoSettingsStore(t *testing.T) {
	h := newSettingsHandler(nil)
	body := url.Values{
		"coinbase_api_key_id": {"key-id"},
		"coinbase_api_secret": {"secret"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/key", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeySave(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandleCoinbaseAPIKeyDisconnect tests (Issue #53) ---
// The spec says: invalid/expired key shows clear error, doesn't crash app.
// Disconnect must clear all Coinbase credentials.

func TestHandleCoinbaseAPIKeyDisconnect_Success(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["coinbase_api_key_id"] = "key-uuid"
	ss.data["coinbase_api_secret"] = "secret"
	ss.data["coinbase_live_balance_sats"] = "500000"
	h := newSettingsHandler(ss)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeyDisconnect(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// All credentials should be cleared
	if ss.data["coinbase_api_key_id"] != "" {
		t.Errorf("coinbase_api_key_id not cleared")
	}
	if ss.data["coinbase_api_secret"] != "" {
		t.Errorf("coinbase_api_secret not cleared")
	}
	if ss.data["coinbase_live_balance_sats"] != "" {
		t.Errorf("coinbase_live_balance_sats not cleared")
	}
}

func TestHandleCoinbaseAPIKeyDisconnect_CallsCallback(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	var cbCalled bool
	var cbKeyID, cbSecret string
	h.OnCoinbaseKeyChange(func(keyID, secret string) {
		cbCalled = true
		cbKeyID = keyID
		cbSecret = secret
	})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeyDisconnect(w, req)

	if !cbCalled {
		t.Error("expected callback to be called on disconnect")
	}
	if cbKeyID != "" || cbSecret != "" {
		t.Errorf("expected empty callback args on disconnect, got keyID=%q secret=%q", cbKeyID, cbSecret)
	}
}

func TestHandleCoinbaseAPIKeyDisconnect_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/coinbase/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeyDisconnect(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleCoinbaseAPIKeyDisconnect_NoSettingsStore(t *testing.T) {
	h := newSettingsHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/coinbase/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseAPIKeyDisconnect(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- SetSettingsStore setter coverage ---

func TestSetSettingsStore(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	ss := newMockSettingsStore()
	h.SetSettingsStore(ss)
	// Verify it was set by exercising a handler that uses it
	req := httptest.NewRequest(http.MethodPost, "/api/settings/strike/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeAPIKeyDisconnect(w, req)
	if w.Code == http.StatusInternalServerError {
		t.Error("expected settings store to be set correctly")
	}
}

// --- HandleSettingsPage tests (#settings) ---

func TestHandleSettingsPage_GET_Renders(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	h.HandleSettingsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
}

func TestHandleSettingsPage_NoSettingsStore_Renders(t *testing.T) {
	// HandleSettingsPage is nil-safe for settingsStore — renders with safe defaults.
	h := newSettingsHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	h.HandleSettingsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even without settings store, got %d", w.Code)
	}
}

func TestHandleSettingsPage_StrikeKeyMasked(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["strike_api_key"] = "sk_live_testkey1234"
	h := newSettingsHandler(ss)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	h.HandleSettingsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Last 4 chars of key ("1234") should appear masked
	if !strings.Contains(w.Body.String(), "1234") {
		t.Error("expected masked Strike key (last 4 chars) in page body")
	}
}

// --- HandlePlansPage tests ---

func TestHandlePlansPage_GET_Renders(t *testing.T) {
	h := newSettingsHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/settings/plans", nil)
	w := httptest.NewRecorder()
	h.HandlePlansPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
}

// --- HandleTelegramSave tests ---

func TestHandleTelegramSave_MethodNotAllowed(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/telegram", nil)
	w := httptest.NewRecorder()
	h.HandleTelegramSave(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleTelegramSave_NoStore_Returns500(t *testing.T) {
	h := newSettingsHandler(nil)
	form := url.Values{"telegram_bot_token": {"tok"}, "telegram_chat_id": {"123"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTelegramSave(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleTelegramSave_EmptyFields_ReturnsErrorFragment(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	form := url.Values{"telegram_bot_token": {""}, "telegram_chat_id": {""}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTelegramSave(w, req)

	// HTMX handler: returns 200 with error HTML fragment (not 4xx)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' in body, got: %s", w.Body.String())
	}
}

func TestHandleTelegramSave_ValidInputs_Saves(t *testing.T) {
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)
	form := url.Values{
		"telegram_bot_token": {"123:ABCDEFbot"},
		"telegram_chat_id":   {"-100123456"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTelegramSave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ss.data["telegram_bot_token"] != "123:ABCDEFbot" {
		t.Errorf("token not saved: %q", ss.data["telegram_bot_token"])
	}
	if ss.data["telegram_chat_id"] != "-100123456" {
		t.Errorf("chat ID not saved: %q", ss.data["telegram_chat_id"])
	}
	// HX-Refresh instructs HTMX to reload the page
	if w.Header().Get("HX-Refresh") != "true" {
		t.Error("expected HX-Refresh: true header")
	}
}

// --- HandleTelegramTest tests ---

func TestHandleTelegramTest_MethodNotAllowed(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/telegram/test", nil)
	w := httptest.NewRecorder()
	h.HandleTelegramTest(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleTelegramTest_NoStore_Returns500(t *testing.T) {
	h := newSettingsHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram/test", nil)
	w := httptest.NewRecorder()
	h.HandleTelegramTest(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleTelegramTest_NotConfigured_ReturnsErrorFragment(t *testing.T) {
	// No bot token stored — should return error fragment
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram/test", nil)
	w := httptest.NewRecorder()
	h.HandleTelegramTest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not configured") {
		t.Errorf("expected 'not configured' in body, got: %s", w.Body.String())
	}
}

func TestHandleTelegramTest_NoSender_ReturnsRestartFragment(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["telegram_bot_token"] = "123:bot"
	ss.data["telegram_chat_id"] = "-100123"
	h := newSettingsHandler(ss)
	// telegramSender is nil by default — simulates not-yet-active client

	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram/test", nil)
	w := httptest.NewRecorder()
	h.HandleTelegramTest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Restart") {
		t.Errorf("expected 'Restart' in body, got: %s", w.Body.String())
	}
}

// --- maskToken unit tests ---

func TestMaskToken_Short(t *testing.T) {
	result := maskToken("ab")
	if result != "****" {
		t.Errorf("maskToken(short) = %q, want ****", result)
	}
}

func TestMaskToken_ExactlyFour(t *testing.T) {
	result := maskToken("abcd")
	if result != "****" {
		t.Errorf("maskToken(exactly 4) = %q, want ****", result)
	}
}

func TestMaskToken_Long(t *testing.T) {
	result := maskToken("sk_live_abcdefgh1234")
	if !strings.HasSuffix(result, "1234") {
		t.Errorf("maskToken(long) = %q, expected suffix '1234'", result)
	}
	if !strings.HasPrefix(result, "********") {
		t.Errorf("maskToken(long) = %q, expected prefix '********'", result)
	}
}

// --- Monarch Money handler tests (Issue #32 and #93: Monarch Money sync) ---
// The spec says: users can connect Monarch via manual token, disconnect, sync holdings,
// configure sync types, and trigger transaction sync.

// mockMonarchSyncer implements MonarchSyncer for testing.
type mockMonarchSyncer struct {
	syncHoldingErr  error
	syncTxResult    *monarchTxSyncResult
	syncTxErr       error
	syncHoldingCalls int
	lastBTCQuantity  float64
}

type monarchTxSyncResult struct {
	created int
	skipped int
	errors  int
	synced  map[string]string
}

func (m *mockMonarchSyncer) SyncHolding(_ context.Context, btcQuantity float64) error {
	m.syncHoldingCalls++
	m.lastBTCQuantity = btcQuantity
	return m.syncHoldingErr
}

func (m *mockMonarchSyncer) SyncTransactions(_ context.Context, txns []monarch.TxToSync) (*monarch.TxSyncResult, map[string]string, error) {
	if m.syncTxErr != nil {
		return nil, nil, m.syncTxErr
	}
	if m.syncTxResult != nil {
		return &monarch.TxSyncResult{
			Created: m.syncTxResult.created,
			Skipped: m.syncTxResult.skipped,
			Errors:  m.syncTxResult.errors,
		}, m.syncTxResult.synced, nil
	}
	return &monarch.TxSyncResult{Created: len(txns)}, make(map[string]string), nil
}

// mockMonarchTxStore implements MonarchTxStore for testing.
type mockMonarchTxStore struct {
	txns          []db.UnifiedTransaction
	listErr       error
	markErr       error
	syncedCount   int
	markedIDs     map[string]string
}

func newMockMonarchTxStore() *mockMonarchTxStore {
	return &mockMonarchTxStore{markedIDs: make(map[string]string)}
}

func (m *mockMonarchTxStore) ListUnsyncedTransactions(_ context.Context, _ []string, _ []string) ([]db.UnifiedTransaction, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.txns, nil
}

func (m *mockMonarchTxStore) MarkTransactionSynced(_ context.Context, sourceID, monarchTxID string) error {
	if m.markErr != nil {
		return m.markErr
	}
	if m.markedIDs == nil {
		m.markedIDs = make(map[string]string)
	}
	m.markedIDs[sourceID] = monarchTxID
	return nil
}

func (m *mockMonarchTxStore) MonarchSyncedCount(_ context.Context) (int, error) {
	return m.syncedCount, nil
}

func (m *mockMonarchTxStore) DistinctTransactionValues(_ context.Context) ([]string, []string, error) {
	return []string{"strike", "river"}, []string{"buy", "sell"}, nil
}

// newMonarchHandler creates a Handler with Monarch dependencies set.
func newMonarchHandler(ss SettingsStore, syncer MonarchSyncer, txStore MonarchTxStore) *Handler {
	store := &mockStore{
		latestWalletFn: func(_ context.Context) (*db.WalletBalanceSnapshot, error) {
			return &db.WalletBalanceSnapshot{TotalSat: 5000000}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{price: 95000})
	if ss != nil {
		h.SetSettingsStore(ss)
	}
	if syncer != nil {
		h.SetMonarchSyncer(syncer)
	}
	if txStore != nil {
		h.SetMonarchTxStore(txStore)
	}
	return h
}

// HandleMonarchToken tests — spec: user can paste a token manually to connect Monarch

func TestHandleMonarchToken_Success(t *testing.T) {
	// Issue #32: runtime connect without restart — manual token path.
	ss := newMockSettingsStore()
	h := newMonarchHandler(ss, nil, nil)

	body := url.Values{"monarch_token": {"some-valid-token-string"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ss.data["monarch_token"] != "some-valid-token-string" {
		t.Errorf("expected token saved, got %q", ss.data["monarch_token"])
	}
	if !strings.Contains(w.Body.String(), "Connected") {
		t.Errorf("expected Connected message, got: %s", w.Body.String())
	}
}

func TestHandleMonarchToken_EmptyToken_ReturnsError(t *testing.T) {
	ss := newMockSettingsStore()
	h := newMonarchHandler(ss, nil, nil)

	body := url.Values{"monarch_token": {""}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") && !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected error message for empty token, got: %s", w.Body.String())
	}
}

func TestHandleMonarchToken_WrongMethod(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/token", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchToken(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMonarchToken_NoStore_Returns500(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	// No settings store set.
	body := url.Values{"monarch_token": {"some-token"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchToken(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 without settings store, got %d", w.Code)
	}
}

func TestHandleMonarchToken_SaveError_ReturnsErrorFragment(t *testing.T) {
	ss := newMockSettingsStore()
	ss.saveErr = fmt.Errorf("db write failure")
	h := newMonarchHandler(ss, nil, nil)

	body := url.Values{"monarch_token": {"valid-token"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "error") && !strings.Contains(w.Body.String(), "Failed") {
		t.Errorf("expected error fragment for save failure, got: %s", w.Body.String())
	}
}

// HandleMonarchDisconnect tests — spec: disconnect clears token and stops background sync

func TestHandleMonarchDisconnect_Success(t *testing.T) {
	// Issue #32: runtime disconnect without restart.
	ss := newMockSettingsStore()
	ss.data["monarch_token"] = "existing-token"
	h := newMonarchHandler(ss, &mockMonarchSyncer{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchDisconnect(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ss.data["monarch_token"] != "" {
		t.Errorf("expected token cleared, got %q", ss.data["monarch_token"])
	}
	if !strings.Contains(w.Body.String(), "disconnected") {
		t.Errorf("expected disconnect message, got: %s", w.Body.String())
	}
}

func TestHandleMonarchDisconnect_WrongMethod(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchDisconnect(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMonarchDisconnect_NoStore_Returns500(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchDisconnect(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleMonarchDisconnect_SaveError_ReturnsErrorFragment(t *testing.T) {
	ss := newMockSettingsStore()
	ss.saveErr = fmt.Errorf("disk full")
	h := newMonarchHandler(ss, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/disconnect", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchDisconnect(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "error") && !strings.Contains(w.Body.String(), "Failed") {
		t.Errorf("expected error fragment for save failure, got: %s", w.Body.String())
	}
}

// HandleMonarchSync tests — spec: syncs current BTC holding to Monarch

func TestHandleMonarchSync_Success(t *testing.T) {
	// Issue #32: holding sync sends total BTC to Monarch.
	mock := &mockMonarchSyncer{}
	h := newMonarchHandler(newMockSettingsStore(), mock, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.syncHoldingCalls != 1 {
		t.Errorf("expected SyncHolding called once, got %d", mock.syncHoldingCalls)
	}
	// 5000000 sats / 1e8 = 0.05 BTC
	if mock.lastBTCQuantity != 0.05 {
		t.Errorf("expected BTC quantity 0.05, got %f", mock.lastBTCQuantity)
	}
	if !strings.Contains(w.Body.String(), "Synced") {
		t.Errorf("expected success message, got: %s", w.Body.String())
	}
}

func TestHandleMonarchSync_NotConnected_ReturnsErrorFragment(t *testing.T) {
	// No syncer set — should show "not connected" error.
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not connected") {
		t.Errorf("expected 'not connected', got: %s", w.Body.String())
	}
}

func TestHandleMonarchSync_SyncError_ReturnsErrorFragment(t *testing.T) {
	mock := &mockMonarchSyncer{syncHoldingErr: fmt.Errorf("api unreachable")}
	h := newMonarchHandler(newMockSettingsStore(), mock, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "failed") && !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected error fragment, got: %s", w.Body.String())
	}
}

func TestHandleMonarchSync_WrongMethod(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSync(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// HandleMonarchSyncTypes tests — spec: saves which transaction types/sources to sync

func TestHandleMonarchSyncTypes_SavesPreferences(t *testing.T) {
	// Issue #93: user selects which types to sync.
	ss := newMockSettingsStore()
	h := newMonarchHandler(ss, nil, nil)

	body := url.Values{
		"sync_types":   {"buy", "sell"},
		"sync_sources": {"strike", "river"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync-types", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchSyncTypes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ss.data["monarch_sync_types"], "buy") {
		t.Errorf("expected sync types saved, got %q", ss.data["monarch_sync_types"])
	}
	if !strings.Contains(ss.data["monarch_sync_sources"], "strike") {
		t.Errorf("expected sync sources saved, got %q", ss.data["monarch_sync_sources"])
	}
}

func TestHandleMonarchSyncTypes_NoTypesSelected_DisablesSyncMessage(t *testing.T) {
	ss := newMockSettingsStore()
	h := newMonarchHandler(ss, nil, nil)

	// Sending no types/sources — disables transaction sync.
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync-types", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchSyncTypes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "disabled") {
		t.Errorf("expected disabled message for no filters, got: %s", w.Body.String())
	}
}

func TestHandleMonarchSyncTypes_WrongMethod(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/sync-types", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSyncTypes(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMonarchSyncTypes_NoStore_Returns500(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	body := url.Values{"sync_types": {"buy"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/sync-types", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchSyncTypes(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// HandleMonarchTxSync tests — spec: pushes BTC transactions to Monarch investment account

func TestHandleMonarchTxSync_NotConnected_ReturnsErrorFragment(t *testing.T) {
	// Issue #93: if not connected, show error.
	h := newMonarchHandler(newMockSettingsStore(), nil, newMockMonarchTxStore())

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not connected") {
		t.Errorf("expected 'not connected', got: %s", w.Body.String())
	}
}

func TestHandleMonarchTxSync_NoTypesSelected_ReturnsErrorFragment(t *testing.T) {
	// No sync types saved — user must configure first.
	ss := newMockSettingsStore()
	// monarch_sync_types is empty
	mock := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	h := newMonarchHandler(ss, mock, txStore)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No transaction types") {
		t.Errorf("expected no-types message, got: %s", w.Body.String())
	}
}

func TestHandleMonarchTxSync_AllAlreadySynced_ReturnsUpToDateMessage(t *testing.T) {
	// Issue #93: deduplication — nothing to push if already synced.
	ss := newMockSettingsStore()
	ss.data["monarch_sync_types"] = "buy,sell"
	mock := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	// txns is empty — all already synced
	h := newMonarchHandler(ss, mock, txStore)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already synced") {
		t.Errorf("expected 'already synced' message, got: %s", w.Body.String())
	}
}

func TestHandleMonarchTxSync_PendingTxns_ReturnsBackgroundMessage(t *testing.T) {
	// Issue #93: transactions to sync — respond immediately, sync in background.
	ss := newMockSettingsStore()
	ss.data["monarch_sync_types"] = "buy"
	mock := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	txStore.txns = []db.UnifiedTransaction{
		{SourceID: "tx-1", Source: "strike", TxType: "buy", AmountUSD: 500},
		{SourceID: "tx-2", Source: "strike", TxType: "buy", AmountUSD: 300},
	}
	h := newMonarchHandler(ss, mock, txStore)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Response immediately with transaction count before background goroutine.
	if !strings.Contains(w.Body.String(), "2") {
		t.Errorf("expected count '2' in response, got: %s", w.Body.String())
	}
}

func TestHandleMonarchTxSync_WrongMethod(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMonarchTxSync_ListError_ReturnsErrorFragment(t *testing.T) {
	ss := newMockSettingsStore()
	ss.data["monarch_sync_types"] = "buy"
	mock := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	txStore.listErr = fmt.Errorf("db connection lost")
	h := newMonarchHandler(ss, mock, txStore)

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Failed") && !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected error fragment, got: %s", w.Body.String())
	}
}

func TestHandleMonarchTxSync_NoTxStoreOrSettings_ReturnsNotAvailable(t *testing.T) {
	// If txStore or settingsStore is nil, should return "not available".
	mock := &mockMonarchSyncer{}
	h := newMonarchHandler(newMockSettingsStore(), mock, nil) // no txStore

	ss := h.settingsStore.(*mockSettingsStore)
	ss.data["monarch_sync_types"] = "buy"

	req := httptest.NewRequest(http.MethodPost, "/api/monarch/tx-sync", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchTxSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not available") {
		t.Errorf("expected 'not available', got: %s", w.Body.String())
	}
}

// HandleMonarchSave tests — spec: login with email/password, OTP optional

func TestHandleMonarchSave_WrongMethod(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/monarch/save", nil)
	w := httptest.NewRecorder()
	h.HandleMonarchSave(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMonarchSave_NoStore_Returns500(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	body := url.Values{"monarch_email": {"a@b.com"}, "monarch_password": {"pass"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/save", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchSave(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleMonarchSave_MissingEmailOrPassword_ReturnsErrorFragment(t *testing.T) {
	h := newMonarchHandler(newMockSettingsStore(), nil, nil)

	// Missing password
	body := url.Values{"monarch_email": {"a@b.com"}}
	req := httptest.NewRequest(http.MethodPost, "/api/monarch/save", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMonarchSave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' error, got: %s", w.Body.String())
	}
}

// OnMonarchChange tests — spec: listener notified when syncer changes

func TestOnMonarchChange_CalledOnSetSyncer(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	var received MonarchSyncer
	h.OnMonarchChange(func(ms MonarchSyncer) {
		received = ms
	})

	mock := &mockMonarchSyncer{}
	h.SetMonarchSyncer(mock)

	if received != mock {
		t.Errorf("expected callback to receive the new syncer")
	}
}

func TestOnMonarchChange_CalledWithNilOnDisconnect(t *testing.T) {
	h := newMonarchHandler(nil, nil, nil)
	callCount := 0
	h.OnMonarchChange(func(ms MonarchSyncer) {
		callCount++
	})

	h.SetMonarchSyncer(nil)
	if callCount != 1 {
		t.Errorf("expected callback called once, got %d", callCount)
	}
}
