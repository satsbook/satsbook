package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/satsbook/satsbook/internal/license"
)

type stubChecker struct {
	tier license.Tier
}

func (s stubChecker) CurrentTier() license.Tier        { return s.tier }
func (s stubChecker) Verify(_ context.Context) error   { return nil }

func TestTierFromContext_Default(t *testing.T) {
	ctx := context.Background()
	if got := TierFromContext(ctx); got != license.TierFree {
		t.Errorf("TierFromContext(empty) = %q, want %q", got, license.TierFree)
	}
}

func TestTierFromContext_WithValue(t *testing.T) {
	ctx := context.WithValue(context.Background(), tierContextKey, license.TierPower)
	if got := TierFromContext(ctx); got != license.TierPower {
		t.Errorf("TierFromContext = %q, want %q", got, license.TierPower)
	}
}

func TestTierMiddleware_InjectsContext(t *testing.T) {
	checker := stubChecker{tier: license.TierPro}

	var capturedTier license.Tier
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTier = TierFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := tierMiddleware(checker, inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if capturedTier != license.TierPro {
		t.Errorf("middleware injected tier = %q, want %q", capturedTier, license.TierPro)
	}
}

func TestRequireTier_Allowed(t *testing.T) {
	renderer := NewRenderer()
	gate := requireTier(license.TierPro, renderer)

	called := false
	handler := gate(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/pro-feature", nil)
	ctx := context.WithValue(req.Context(), tierContextKey, license.TierPower)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("expected handler to be called when tier is sufficient")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireTier_Blocked_API(t *testing.T) {
	renderer := NewRenderer()
	gate := requireTier(license.TierPro, renderer)

	called := false
	handler := gate(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/pro-feature", nil)
	ctx := context.WithValue(req.Context(), tierContextKey, license.TierFree)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler should NOT be called when tier is insufficient")
	}
	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusPaymentRequired)
	}
}

func TestRequireTier_Blocked_HTMX(t *testing.T) {
	renderer := NewRenderer()
	gate := requireTier(license.TierPower, renderer)

	handler := gate(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should NOT be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/power-feature", nil)
	req.Header.Set("HX-Request", "true")
	ctx := context.WithValue(req.Context(), tierContextKey, license.TierPro)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// HTMX blocked requests return 200 with upgrade partial
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d for HTMX upgrade partial", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestRequireTier_ExactTier(t *testing.T) {
	renderer := NewRenderer()
	gate := requireTier(license.TierPro, renderer)

	called := false
	handler := gate(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/pro-feature", nil)
	ctx := context.WithValue(req.Context(), tierContextKey, license.TierPro)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called when tier equals required tier")
	}
}
