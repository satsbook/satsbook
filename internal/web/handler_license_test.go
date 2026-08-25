package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// errTestSave is a reusable error for save failure tests.
var errTestSave = errors.New("db write failure")

// --- HandleLicenseActivate tests (Issue #13: License key system) ---
// The spec says: users can enter a license key in settings UI to activate Pro/Power tier.

func TestHandleLicenseActivate_NoKey_ReturnsErrorFragment(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())

	req := httptest.NewRequest(http.MethodPost, "/api/settings/license/activate", nil)
	w := httptest.NewRecorder()
	h.HandleLicenseActivate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "required") {
		t.Errorf("expected 'required' error, got: %s", w.Body.String())
	}
}

func TestHandleLicenseActivate_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/license/activate", nil)
	w := httptest.NewRecorder()
	h.HandleLicenseActivate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleLicenseActivate_SavesKey_NoChecker(t *testing.T) {
	// Without a licenseChecker, the key is saved and a "restart" message is shown.
	ss := newMockSettingsStore()
	h := newSettingsHandler(ss)

	body := url.Values{"license_key": {"SATS-PRO-ABCD-1234"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/license/activate", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleLicenseActivate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ss.data["license_key"] != "SATS-PRO-ABCD-1234" {
		t.Errorf("expected key saved, got %q", ss.data["license_key"])
	}
	// Without checker, returns success message about saving (restart to activate).
	if !strings.Contains(w.Body.String(), "saved") {
		t.Errorf("expected 'saved' message, got: %s", w.Body.String())
	}
}

func TestHandleLicenseActivate_SaveError_ReturnsErrorFragment(t *testing.T) {
	ss := newMockSettingsStore()
	ss.saveErr = errTestSave
	h := newSettingsHandler(ss)

	body := url.Values{"license_key": {"SATS-PRO-ABCD-1234"}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/license/activate", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleLicenseActivate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "error") && !strings.Contains(w.Body.String(), "Failed") {
		t.Errorf("expected error fragment, got: %s", w.Body.String())
	}
}

// --- HandleLicenseVerify tests (Issue #13: License key system) ---

func TestHandleLicenseVerify_WrongMethod(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/license/verify", nil)
	w := httptest.NewRecorder()
	h.HandleLicenseVerify(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleLicenseVerify_NoChecker_ReturnsErrorFragment(t *testing.T) {
	// Without a checker configured, should return an error HTML fragment (HTMX pattern).
	h := newSettingsHandler(newMockSettingsStore())

	req := httptest.NewRequest(http.MethodPost, "/api/settings/license/verify", nil)
	w := httptest.NewRecorder()
	h.HandleLicenseVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error fragment, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No license checker") && !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected error about no checker, got: %s", w.Body.String())
	}
}

// --- HandleSubscribe tests (Issue #107: In-app upgrade flow) ---
// The spec says: users can initiate Stripe checkout for Pro or Power tier from settings.

func TestHandleSubscribe_InvalidTier_Returns400(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=free", nil)
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid tier, got %d", w.Code)
	}
}

func TestHandleSubscribe_NoCheckoutURL_ReturnsServiceUnavailable(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	// checkoutBaseURL not set

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=pro", nil)
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when checkout not configured, got %d", w.Code)
	}
}

func TestHandleSubscribe_PowerTier_NoCheckoutURL_ReturnsServiceUnavailable(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=power", nil)
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for power tier without checkout, got %d", w.Code)
	}
}

func TestHandleSubscribe_WithCheckoutServer_RedirectsToStripe(t *testing.T) {
	// Mock the license server returning a Stripe URL.
	stripeURL := "https://checkout.stripe.com/pay/cs_test_abc123"
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": stripeURL})
	}))
	defer checkout.Close()

	h := newSettingsHandler(newMockSettingsStore())
	h.SetCheckoutBaseURL(checkout.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=pro", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != stripeURL {
		t.Errorf("expected redirect to %q, got %q", stripeURL, loc)
	}
}

func TestHandleSubscribe_CheckoutServerError_ReturnsBadGateway(t *testing.T) {
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer checkout.Close()

	h := newSettingsHandler(newMockSettingsStore())
	h.SetCheckoutBaseURL(checkout.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=pro", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 when checkout server errors, got %d", w.Code)
	}
}

func TestHandleSubscribe_CheckoutServerUnreachable_ReturnsBadGateway(t *testing.T) {
	h := newSettingsHandler(newMockSettingsStore())
	h.SetCheckoutBaseURL("http://127.0.0.1:1") // nothing listening

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=power", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for unreachable checkout server, got %d", w.Code)
	}
}

func TestHandleSubscribe_CheckoutServerBadJSON_ReturnsBadGateway(t *testing.T) {
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"url": ""}`)) // empty URL
	}))
	defer checkout.Close()

	h := newSettingsHandler(newMockSettingsStore())
	h.SetCheckoutBaseURL(checkout.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/subscribe?tier=pro", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.HandleSubscribe(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for empty URL in response, got %d", w.Code)
	}
}

