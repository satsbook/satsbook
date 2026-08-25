package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
