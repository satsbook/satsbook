package web

import (
	"net/http"

	"github.com/satsbook/satsbook/internal/license"
)

type TaxGuidePageData struct {
	IsPro bool
}

// HandleTaxGuidePage serves GET /tax-guide.
// This is a public marketing/content page — no auth or tier check required.
func (h *Handler) HandleTaxGuidePage(w http.ResponseWriter, r *http.Request) {
	tier := TierFromContext(r.Context())
	data := TaxGuidePageData{
		IsPro: license.TierAtLeast(tier, license.TierPro),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "tax_guide_layout", data); err != nil {
		h.logger.Printf("failed to render tax guide page: %v", err)
	}
}
