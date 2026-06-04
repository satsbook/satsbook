package web

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/license"
)

// Renderer parses and renders HTML templates from the embedded filesystem.
type Renderer struct {
	templates *template.Template
}

// NewRenderer parses all templates from the embedded FS.
// It panics if templates cannot be parsed (fail-fast at startup).
func NewRenderer() *Renderer {
	funcMap := template.FuncMap{
		"formatSats":  formatSats,
		"formatUSD":   formatUSD,
		"timeAgo":     timeAgo,
		"formatDate":  formatDate,
		"msatToSat":   func(msat int64) int64 { return msat / 1000 },
		"satsToBTC":   func(sats int64) string { return fmt.Sprintf("%.8f", float64(sats)/100_000_000.0) },
		"pctOf":       pctOf,
		"feeChart":    feeChart,
		"sub":         func(a, b int) int { return a - b },
		"subtract":    func(a, b int) int { return a - b },
		"timeAgoPtr":  func(t *time.Time) string {
			if t == nil {
				return "never"
			}
			return timeAgo(*t)
		},
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n]
		},
		"add": func(a, b int) int { return a + b },
		"add64":       func(a, b int64) int64 { return a + b },
		"sub64":       func(a, b int64) int64 { return a - b },
		"int64":       func(n int) int64 { return int64(n) },
		"seq":         seq,
		"netSats": func(s *db.ExchangeSummaryResult) int64 {
			if s == nil {
				return 0
			}
			return s.PurchasedSats + s.ReceivedSats - s.SoldSats - s.SentSats
		},
		"costPerBTC": func(costUSD float64, sats int64) float64 {
			if sats <= 0 {
				return 0
			}
			return costUSD / (float64(sats) / 1e8)
		},
		"portfolioChart":       portfolioChart,
		"donutChart":           donutChart,
		"portfolioSourceLabel": portfolioSourceLabel,
		"tierAtLeast": func(current, required string) bool {
			return license.TierAtLeast(license.Tier(current), license.Tier(required))
		},
		"sortIndicator": func(col, currentCol, currentDir string) template.HTML {
			if col != currentCol {
				return template.HTML(`<span style="opacity:0.3">&#x21C5;</span>`)
			}
			if currentDir == "asc" {
				return template.HTML(`<span>&#x25B2;</span>`)
			}
			return template.HTML(`<span>&#x25BC;</span>`)
		},
		"flipDir": func(col, currentCol, currentDir string) string {
			if col != currentCol {
				return "asc"
			}
			if currentDir == "asc" {
				return "desc"
			}
			return "asc"
		},
		"contains": func(slice []string, val string) bool {
			for _, s := range slice {
				if s == val {
					return true
				}
			}
			return false
		},
		"formatRFC3339": func(t time.Time) string { return t.Format(time.RFC3339) },
		"sourceLabel": templateSourceLabel,
		"dict": func(kv ...interface{}) (map[string]interface{}, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict requires even number of args")
			}
			m := make(map[string]interface{}, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
	}

	tmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/layout.html",
			"templates/dashboard.html",
			"templates/import_layout.html",
			"templates/import.html",
			"templates/partials/forwarding_table.html",
			"templates/partials/channel_table.html",
			"templates/partials/import_result.html",
			"templates/partials/clear_import_result.html",
			"templates/partials/danger_zone.html",
			"templates/partials/nav.html",
			"templates/lightning_layout.html",
			"templates/lightning.html",
			"templates/pl_layout.html",
			"templates/pl.html",
			"templates/wallets_layout.html",
			"templates/exchange_detail.html",
			"templates/wallet_detail.html",
			"templates/tax_layout.html",
			"templates/tax.html",
			"templates/portfolio_layout.html",
			"templates/portfolio.html",
			"templates/settings_layout.html",
			"templates/transactions_layout.html",
			"templates/partials/transaction_note.html",
			"templates/partials/upgrade_required.html",
			"templates/partials/transfer_badge.html",
			"templates/partials/transfer_candidates.html",
		),
	)

	return &Renderer{templates: tmpl}
}

// Render executes the named template with the given data.
func (r *Renderer) Render(w io.Writer, name string, data interface{}) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

// --- Template helper functions ---

// formatSats formats an integer with comma separators (e.g., 1234567 -> "1,234,567").
func formatSats(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}

	parts := make([]string, 0, (len(s)-1)/3+1)
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}

	result := strings.Join(parts, ",")
	if n < 0 {
		return "-" + result
	}
	return result
}

// formatUSD formats a float as USD (e.g., 1234.56 -> "$1,234.56").
func formatUSD(f float64) string {
	cents := fmt.Sprintf("%.2f", f)
	// Split on decimal
	dotIdx := strings.Index(cents, ".")
	intPart := cents[:dotIdx]
	decPart := cents[dotIdx:]

	negative := false
	if strings.HasPrefix(intPart, "-") {
		negative = true
		intPart = intPart[1:]
	}

	parts := make([]string, 0, (len(intPart)-1)/3+1)
	for i := len(intPart); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{intPart[start:i]}, parts...)
	}

	result := "$" + strings.Join(parts, ",") + decPart
	if negative {
		return "-" + result
	}
	return result
}

// timeAgo returns a human-readable relative time string.
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// formatDate formats a time as "Jan 02, 2006".
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("Jan 02, 2006")
}

// pctOf calculates percentage, returning 0 if total is 0.
func pctOf(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// seq returns a slice of ints from 0 to n-1 (for range iteration in templates).
func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// templateSourceLabel returns a human-readable label for a transaction source.
func templateSourceLabel(source string) string {
	switch source {
	case "strike":
		return "Strike"
	case "river":
		return "River"
	case "coinbase":
		return "Coinbase"
	case "swan":
		return "Swan"
	case "lnd_forward":
		return "Lightning Routing"
	case "lnd_invoice":
		return "Lightning Invoice"
	case "lnd_payment":
		return "Lightning Payment"
	case "lnd_onchain":
		return "On-chain"
	default:
		return source
	}
}

// portfolioChart generates an inline SVG line chart from portfolio snapshots.
func portfolioChart(snapshots []db.PortfolioSnapshot) template.HTML {
	if len(snapshots) == 0 {
		return template.HTML(`<div class="chart-empty">No portfolio data yet — snapshots are recorded each sync cycle</div>`)
	}

	const (
		width     = 800
		height    = 200
		padTop    = 10
		padBottom = 30
		padLeft   = 70
		padRight  = 10
		chartW    = width - padLeft - padRight
		chartH    = height - padTop - padBottom
	)

	// Find min/max sats for scaling
	var minSats, maxSats int64
	minSats = snapshots[0].TotalSats
	maxSats = snapshots[0].TotalSats
	for _, s := range snapshots {
		if s.TotalSats < minSats {
			minSats = s.TotalSats
		}
		if s.TotalSats > maxSats {
			maxSats = s.TotalSats
		}
	}

	// Add 5% padding to range
	rangeVal := maxSats - minSats
	if rangeVal == 0 {
		rangeVal = 1
	}
	pad := rangeVal / 20
	if pad == 0 {
		pad = 1
	}
	scaleMin := minSats - pad
	if scaleMin < 0 {
		scaleMin = 0
	}
	scaleMax := maxSats + pad
	scaleRange := scaleMax - scaleMin
	if scaleRange == 0 {
		scaleRange = 1
	}

	n := len(snapshots)
	xStep := float64(chartW) / float64(n-1)
	if n == 1 {
		xStep = float64(chartW)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">`, width, height)

	// Y-axis and X-axis
	fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, padLeft, padTop, padLeft, padTop+chartH)
	fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, padLeft, padTop+chartH, padLeft+chartW, padTop+chartH)

	// Y-axis labels
	fmt.Fprintf(&sb, `<text x="%d" y="%d" class="chart-label" text-anchor="end">%s</text>`, padLeft-5, padTop+10, formatSats(scaleMax))
	fmt.Fprintf(&sb, `<text x="%d" y="%d" class="chart-label" text-anchor="end">%s</text>`, padLeft-5, padTop+chartH, formatSats(scaleMin))

	// Build polyline points and circles
	var points strings.Builder
	for i, s := range snapshots {
		var x float64
		if n == 1 {
			x = float64(padLeft) + float64(chartW)/2
		} else {
			x = float64(padLeft) + float64(i)*xStep
		}
		y := float64(padTop) + float64(chartH) - (float64(s.TotalSats-scaleMin)/float64(scaleRange))*float64(chartH)
		fmt.Fprintf(&points, "%.1f,%.1f ", x, y)

		// Tooltip circle for each point
		usdLabel := ""
		if s.BTCPriceUSD > 0 {
			usdVal := float64(s.TotalSats) / 1e8 * s.BTCPriceUSD
			usdLabel = fmt.Sprintf(" ($%.0f)", usdVal)
		}
		fmt.Fprintf(&sb, `<circle cx="%.1f" cy="%.1f" r="3" class="chart-dot"><title>%s: %s sats%s</title></circle>`,
			x, y, s.CapturedAt.Format("Jan 02"), formatSats(s.TotalSats), usdLabel)

		// X-axis labels
		if i == 0 || i == n-1 || (n > 7 && i%(n/5) == 0) {
			fmt.Fprintf(&sb, `<text x="%.1f" y="%d" class="chart-label" text-anchor="middle">%s</text>`,
				x, padTop+chartH+15, s.CapturedAt.Format("01-02"))
		}
	}

	// Draw the line
	fmt.Fprintf(&sb, `<polyline points="%s" class="chart-line" fill="none" stroke="var(--accent, #f7931a)" stroke-width="2"/>`,
		strings.TrimSpace(points.String()))

	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

// feeChart generates an inline SVG bar chart from daily fee data.
func feeChart(dailyFees []db.DailyFeeStat) template.HTML {
	if len(dailyFees) == 0 {
		return template.HTML(`<div class="chart-empty">No forwarding data yet</div>`)
	}

	const (
		width      = 800
		height     = 200
		padTop     = 10
		padBottom  = 30
		padLeft    = 60
		padRight   = 10
		chartW     = width - padLeft - padRight
		chartH     = height - padTop - padBottom
	)

	// Find max fee for scaling
	var maxFee int64
	for _, d := range dailyFees {
		sats := d.TotalFeeMsat / 1000
		if sats > maxFee {
			maxFee = sats
		}
	}
	if maxFee == 0 {
		maxFee = 1 // avoid divide by zero
	}

	n := len(dailyFees)
	barW := float64(chartW) / float64(n)
	gap := barW * 0.2
	if gap < 1 {
		gap = 1
	}
	actualBarW := barW - gap

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">`, width, height)

	// Y-axis line
	fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, padLeft, padTop, padLeft, padTop+chartH)
	// X-axis line
	fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, padLeft, padTop+chartH, padLeft+chartW, padTop+chartH)

	// Y-axis labels (0 and max)
	fmt.Fprintf(&sb, `<text x="%d" y="%d" class="chart-label" text-anchor="end">%s</text>`, padLeft-5, padTop+10, formatSats(maxFee))
	fmt.Fprintf(&sb, `<text x="%d" y="%d" class="chart-label" text-anchor="end">0</text>`, padLeft-5, padTop+chartH)

	// Bars
	for i, d := range dailyFees {
		sats := d.TotalFeeMsat / 1000
		barH := float64(sats) / float64(maxFee) * float64(chartH)
		if sats > 0 && barH < 2 {
			barH = 2 // minimum visible height for non-zero
		}
		x := float64(padLeft) + float64(i)*barW + gap/2
		y := float64(padTop) + float64(chartH) - barH

		fmt.Fprintf(&sb, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="chart-bar"><title>%s: %s sats (%d events)</title></rect>`,
			x, y, actualBarW, barH, d.Day, formatSats(sats), d.Count)

		// X-axis label every 7th day, or first and last
		if i == 0 || i == n-1 || (n > 7 && i%(n/5) == 0) {
			label := d.Day
			if len(label) >= 10 {
				label = label[5:] // "MM-DD"
			}
			labelX := x + actualBarW/2
			fmt.Fprintf(&sb, `<text x="%.1f" y="%d" class="chart-label" text-anchor="middle">%s</text>`,
				labelX, padTop+chartH+15, label)
		}
	}

	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

// donutChartColors are distinct colors for portfolio source segments.
var donutChartColors = []string{
	"#f7931a", // orange (bitcoin)
	"#4fc3f7", // light blue
	"#66bb6a", // green
	"#ab47bc", // purple
	"#ef5350", // red
	"#ffa726", // amber
	"#26c6da", // cyan
}

// donutChart generates an inline SVG donut chart from breakdown items.
func donutChart(items []BreakdownItem) template.HTML {
	if len(items) == 0 {
		return template.HTML(`<div class="chart-empty">No portfolio data</div>`)
	}

	const (
		size    = 200
		cx      = 100
		cy      = 100
		radius  = 80
		strokeW = 30
	)
	circumf := 2 * 3.14159265 * float64(radius)

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" style="max-width:200px">`, size, size)

	// Background circle
	fmt.Fprintf(&sb, `<circle cx="%d" cy="%d" r="%d" fill="none" stroke="var(--border, #333)" stroke-width="%d"/>`,
		cx, cy, radius, strokeW)

	offset := 0.0
	for i, item := range items {
		if item.Pct <= 0 {
			continue
		}
		dashLen := circumf * item.Pct / 100
		dashGap := circumf - dashLen
		color := donutChartColors[i%len(donutChartColors)]

		fmt.Fprintf(&sb, `<circle cx="%d" cy="%d" r="%d" fill="none" stroke="%s" stroke-width="%d" `+
			`stroke-dasharray="%.1f %.1f" stroke-dashoffset="%.1f" transform="rotate(-90 %d %d)">`+
			`<title>%s: %.1f%%</title></circle>`,
			cx, cy, radius, color, strokeW,
			dashLen, dashGap, -offset,
			cx, cy,
			item.Label, item.Pct)
		offset += dashLen
	}

	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

// portfolioSourceLabel returns a human-readable label for a portfolio snapshot source key.
func portfolioSourceLabel(source string) string {
	switch source {
	case "onchain":
		return "On-chain"
	case "channels":
		return "Channels"
	case "cold_storage":
		return "Cold Storage"
	default:
		return templateSourceLabel(source)
	}
}
