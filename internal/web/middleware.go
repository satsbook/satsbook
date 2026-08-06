package web

import (
	"context"
	"net/http"

	"github.com/satsbook/satsbook/internal/license"
)

type contextKey string

const tierContextKey contextKey = "tier"

// TierFromContext extracts the license tier from the request context.
func TierFromContext(ctx context.Context) license.Tier {
	if t, ok := ctx.Value(tierContextKey).(license.Tier); ok {
		return t
	}
	return license.TierFree
}

// tierMiddleware injects the current license tier into every request's context.
func tierMiddleware(checker license.Checker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), tierContextKey, checker.CurrentTier())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireTier wraps a handler and blocks access unless the user's tier meets the minimum.
// For HTMX requests it returns a 200 with an upgrade partial; for API requests it returns 402.
func requireTier(minTier license.Tier, renderer *Renderer) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			current := TierFromContext(r.Context())
			if license.TierAtLeast(current, minTier) {
				next(w, r)
				return
			}

			// Blocked — render upgrade prompt.
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				data := upgradeData{
					RequiredTier: string(minTier),
					CurrentTier:  string(current),
				}
				if err := renderer.Render(w, "upgrade_required", data); err != nil {
					http.Error(w, "upgrade required", http.StatusPaymentRequired)
				}
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(`{"error":"upgrade required","required_tier":"` + string(minTier) + `"}`))
		}
	}
}

type upgradeData struct {
	RequiredTier   string
	CurrentTier    string
	FeatureMessage string
}
