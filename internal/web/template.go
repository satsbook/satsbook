package web

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/db"
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
		"add":         func(a, b int) int { return a + b },
		"add64":       func(a, b int64) int64 { return a + b },
		"seq":         seq,
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
