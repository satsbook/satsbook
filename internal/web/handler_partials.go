package web

import (
	"net/http"
	"time"
)

// HandlePortfolioChartPartial serves GET /partials/portfolio-chart (HTMX partial).
func (h *Handler) HandlePortfolioChartPartial(w http.ResponseWriter, r *http.Request) {
	days := parseIntParam(r, "days", 30)
	if days <= 0 {
		days = 3650 // "All" — 10 years
	}

	snaps, err := h.store.PortfolioSnapshots(r.Context(), days)
	if err != nil {
		h.logger.Printf("portfolio chart partial error: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// portfolioChart returns template.HTML, write it directly
	html := portfolioChart(snaps)
	w.Write([]byte(html))
}

// HandleForwardingPartial serves GET /partials/forwarding (HTMX partial).
func (h *Handler) HandleForwardingPartial(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	now := time.Now()
	from, err := parseDateParam(r, "from", now.AddDate(0, 0, -30))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'from' date")
		return
	}
	to, err := parseDateParam(r, "to", now)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'to' date")
		return
	}
	// Extend 'to' to end of day
	to = to.Add(24*time.Hour - time.Second)

	page := parseIntParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := parseIntParam(r, "limit", 20)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	result, err := h.store.ForwardingEvents(ctx, from, to, limit, offset)
	if err != nil {
		h.logger.Printf("failed to query forwarding events: %v", err)
		http.Error(w, "failed to load forwarding events", http.StatusInternalServerError)
		return
	}

	pageStart := offset + 1
	if result.Total == 0 {
		pageStart = 0
	}
	pageEnd := offset + len(result.Events)

	data := ForwardingTableData{
		Events:    result.Events,
		Page:      page,
		Limit:     limit,
		Total:     result.Total,
		PageStart: pageStart,
		PageEnd:   pageEnd,
		HasNext:   int64(pageEnd) < result.Total,
		From:      r.URL.Query().Get("from"),
		To:        r.URL.Query().Get("to"),
	}
	if data.From == "" {
		data.From = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	if data.To == "" {
		data.To = now.Format("2006-01-02")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "forwarding_table", data); err != nil {
		h.logger.Printf("failed to render forwarding partial: %v", err)
	}
}
