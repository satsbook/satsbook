package web

import (
	"net/http"
	"time"

	"github.com/satsbook/satsbook/internal/license"
)

type TaxGuidePageData struct {
	NodeAlias      string
	NodeSynced     bool
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
	Tier           string
	IsPro          bool
}

// HandleTaxGuidePage serves GET /tax-guide.
// This is a public marketing/content page — no auth or tier check required.
func (h *Handler) HandleTaxGuidePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tier := TierFromContext(ctx)
	data := TaxGuidePageData{
		Tier:  string(tier),
		IsPro: license.TierAtLeast(tier, license.TierPro),
	}

	if info := h.getNodeInfo(ctx); info != nil {
		data.NodeAlias = info.Alias
		data.NodeSynced = info.Synced
	}
	data.LastSyncedAt, _ = h.store.LastSyncedAt(ctx)
	if price, err := h.price.GetBTCPrice(ctx); err == nil {
		data.BTCPriceUSD = price
		data.PriceFetchedAt = h.price.FetchedAt()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "tax_guide_layout", data); err != nil {
		h.logger.Printf("failed to render tax guide page: %v", err)
	}
}
