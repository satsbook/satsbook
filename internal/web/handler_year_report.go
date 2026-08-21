package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/license"
)

// YearReportPageData holds all data for the /year/{year} page.
type YearReportPageData struct {
	NodeAlias      string
	NodePubKey     string
	NodeSynced     bool
	BlockHeight    uint32
	LNDVersion     string
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
	Tier           string
	IsPro          bool

	Year           int
	AvailableYears []int

	// Routing (free)
	TotalFeeSats   int64
	RoutedCount    int64
	RoutedVolSats  int64
	MonthlyFees    []db.MonthlyFeeStat
	BestChanID     string
	BestChanFee    int64 // sats
	MaxMonthlyFee  int64 // sats — used for chart scaling

	// Acquisitions (pro)
	AcquiredSats   int64
	AcquisitionUSD float64
	DisposalCount  int
}

// HandleYearReportPage serves GET /year and GET /year/{year}.
func (h *Handler) HandleYearReportPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse year from URL: /year/2026 or /year/
	yearStr := strings.TrimPrefix(r.URL.Path, "/year")
	yearStr = strings.TrimPrefix(yearStr, "/")
	year := time.Now().Year()
	if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= time.Now().Year() {
		year = y
	}

	tier := TierFromContext(ctx)
	data := YearReportPageData{
		Year:  year,
		Tier:  string(tier),
		IsPro: license.TierAtLeast(tier, license.TierPro),
	}

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

	if years, err := h.store.AvailableReportYears(ctx); err == nil {
		data.AvailableYears = years
	}

	report, err := h.store.AnnualReport(ctx, year)
	if err != nil {
		h.logger.Printf("annual report query failed: %v", err)
	} else {
		data.TotalFeeSats = report.TotalFeeMsat / 1000
		data.RoutedCount = report.RoutedCount
		data.RoutedVolSats = report.RoutedVolMsat / 1000
		data.MonthlyFees = report.MonthlyFees
		data.BestChanID = report.BestChanID
		data.BestChanFee = report.BestChanFee / 1000
		data.AcquiredSats = report.AcquiredSats
		data.AcquisitionUSD = report.AcquisitionUSD
		data.DisposalCount = report.DisposalCount

		for _, m := range report.MonthlyFees {
			if s := m.TotalFeeMsat / 1000; s > data.MaxMonthlyFee {
				data.MaxMonthlyFee = s
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "year_report_layout", data); err != nil {
		h.logger.Printf("failed to render year report page: %v", err)
	}
}
