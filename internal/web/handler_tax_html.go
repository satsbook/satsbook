package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/satsbook/satsbook/internal/tax"
)

// TaxPageData holds data for the /tax page.
type TaxPageData struct {
	NodeAlias      string
	NodePubKey     string
	NodeSynced     bool
	BlockHeight    uint32
	LNDVersion     string
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
	Tier           string

	Method       string
	SelectedYear int
	Years        []int

	// Summary fields
	TotalProceeds     float64
	TotalCostBasis    float64
	TotalGainLoss     float64
	ShortTermGainLoss float64
	LongTermGainLoss  float64
	ShortTermCount    int
	LongTermCount     int
	TotalDisposals    int
	UnmatchedSat      int64
}

// HandleTaxPage serves GET /tax.
func (h *Handler) HandleTaxPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	method := r.URL.Query().Get("method")
	if method != "lifo" {
		method = "fifo"
	}

	yearStr := r.URL.Query().Get("year")
	selectedYear := 0
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil {
			selectedYear = y
		}
	}

	data := TaxPageData{
		Method:       method,
		SelectedYear: selectedYear,
	}

	// Fill node/price info
	if info := h.getNodeInfo(ctx); info != nil {
		data.NodeAlias = info.Alias
		data.NodePubKey = info.PubKey
		data.NodeSynced = info.Synced
		data.BlockHeight = info.BlockHeight
		data.LNDVersion = info.Version
	}
	data.LastSyncedAt, _ = h.store.LastSyncedAt(ctx)
	if price, err := h.price.GetBTCPrice(ctx); err == nil {
		data.BTCPriceUSD = price
		data.PriceFetchedAt = h.price.FetchedAt()
	}
	data.Tier = string(TierFromContext(ctx))

	// Generate year options
	now := time.Now()
	for y := now.Year(); y >= now.Year()-5; y-- {
		data.Years = append(data.Years, y)
	}

	// Compute tax summary if taxStore is available
	if h.taxStore != nil {
		dbLots, err := h.taxStore.ListBTCLots(ctx)
		if err == nil {
			dbDisposals, err := h.taxStore.ListDisposals(ctx)
			if err == nil {
				lots := make([]tax.Lot, len(dbLots))
				for i, l := range dbLots {
					lots[i] = tax.Lot{
						ID: l.ID, AcquiredAt: l.AcquiredAt, AmountSat: l.AmountSat,
						PriceUSD: l.PriceUSD, Source: l.Source, ExternalID: l.ExternalID,
					}
				}
				disposals := make([]tax.Disposal, len(dbDisposals))
				for i, d := range dbDisposals {
					disposals[i] = tax.Disposal{
						DisposedAt: d.DisposedAt, AmountSat: d.AmountSat, ProceedsUSD: d.ProceedsUSD,
						TxType: d.TxType, Source: d.Source, ExternalID: d.ExternalID,
					}
				}

				taxMethod := tax.FIFO
				if method == "lifo" {
					taxMethod = tax.LIFO
				}

				result, err := tax.Match(lots, disposals, taxMethod)
				if err == nil {
					// Filter by year if specified
					if selectedYear > 0 {
						var filtered []tax.TaxableEvent
						for _, e := range result.Events {
							if e.DisposedAt.Year() == selectedYear {
								filtered = append(filtered, e)
							}
						}
						result.Events = filtered
					}

					summary := tax.Summarize(result)
					data.TotalProceeds = summary.TotalProceeds
					data.TotalCostBasis = summary.TotalCostBasis
					data.TotalGainLoss = summary.TotalGainLoss
					data.ShortTermGainLoss = summary.ShortTermGainLoss
					data.LongTermGainLoss = summary.LongTermGainLoss
					data.ShortTermCount = summary.ShortTermCount
					data.LongTermCount = summary.LongTermCount
					data.TotalDisposals = summary.TotalDisposals
					data.UnmatchedSat = summary.UnmatchedSat
				}
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "tax_layout", data); err != nil {
		h.logger.Printf("failed to render tax page: %v", err)
	}
}
