package web

import (
	"net/http"
)

// HandleTaxGuidePage serves GET /tax-guide.
// This is a public marketing/content page — no auth or tier check required.
func (h *Handler) HandleTaxGuidePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "tax_guide_layout", nil); err != nil {
		h.logger.Printf("failed to render tax guide page: %v", err)
	}
}
