package web

import (
	"net/http"
	"time"
)

// ImportPageData is passed to the import page template.
type ImportPageData struct {
	HasStrike   bool
	HasRiver    bool
	HasCoinbase bool
	HasSwan     bool

	// Header / footer
	NodeAlias      string
	NodeSynced     bool
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
	Tier           string
}

// HandleImportPage serves GET /import.
func (h *Handler) HandleImportPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := ImportPageData{
		Tier: string(TierFromContext(ctx)),
	}
	if info := h.getNodeInfo(ctx); info != nil {
		data.NodeAlias = info.Alias
		data.NodeSynced = info.Synced
	}
	if t, err := h.store.LastSyncedAt(ctx); err == nil {
		data.LastSyncedAt = t
	}
	if price, err := h.price.GetBTCPrice(ctx); err == nil {
		data.BTCPriceUSD = price
		data.PriceFetchedAt = h.price.FetchedAt()
	}
	// Query per-source presence. We only care whether the vendor has any
	// rows so the danger zone can be revealed; any error here is non-fatal.
	if pos, err := h.store.PortfolioPosition(ctx, time.Time{}); err == nil && pos != nil {
		if s, ok := pos.BySource["strike"]; ok && (s.NetSats != 0 || s.PurchasedSats != 0) {
			data.HasStrike = true
		}
		if s, ok := pos.BySource["river"]; ok && (s.NetSats != 0 || s.PurchasedSats != 0) {
			data.HasRiver = true
		}
		if s, ok := pos.BySource["coinbase"]; ok && (s.NetSats != 0 || s.PurchasedSats != 0) {
			data.HasCoinbase = true
		}
		if s, ok := pos.BySource["swan"]; ok && (s.NetSats != 0 || s.PurchasedSats != 0) {
			data.HasSwan = true
		}
	} else if err != nil {
		h.logger.Printf("import page: portfolio position lookup failed: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "import_layout", data); err != nil {
		h.logger.Printf("failed to render import page: %v", err)
	}
}
