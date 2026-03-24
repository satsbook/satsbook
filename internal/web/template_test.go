package web

import (
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

func TestNewRenderer_ParsesAllTemplates(t *testing.T) {
	// Should not panic
	r := NewRenderer()
	if r.templates == nil {
		t.Fatal("expected templates to be parsed")
	}
}

func TestFormatSats(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{100, "100"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{100000000, "100,000,000"},
		{-1234, "-1,234"},
	}
	for _, tt := range tests {
		got := formatSats(tt.input)
		if got != tt.expected {
			t.Errorf("formatSats(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0.0, "$0.00"},
		{1.5, "$1.50"},
		{67000.0, "$67,000.00"},
		{1234567.89, "$1,234,567.89"},
		{0.01, "$0.01"},
		{-42.50, "-$42.50"},
	}
	for _, tt := range tests {
		got := formatUSD(tt.input)
		if got != tt.expected {
			t.Errorf("formatUSD(%f) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		input    time.Time
		contains string
	}{
		{time.Time{}, "never"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-1 * time.Minute), "1 minute ago"},
		{now.Add(-5 * time.Minute), "5 minutes ago"},
		{now.Add(-1 * time.Hour), "1 hour ago"},
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-25 * time.Hour), "1 day ago"},
		{now.Add(-72 * time.Hour), "3 days ago"},
	}
	for _, tt := range tests {
		got := timeAgo(tt.input)
		if got != tt.contains {
			t.Errorf("timeAgo(%v) = %q, want %q", tt.input, got, tt.contains)
		}
	}
}

func TestFormatDate(t *testing.T) {
	got := formatDate(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC))
	if got != "Mar 15, 2024" {
		t.Errorf("expected 'Mar 15, 2024', got %q", got)
	}
	if formatDate(time.Time{}) != "-" {
		t.Error("expected '-' for zero time")
	}
}

func TestPctOf(t *testing.T) {
	if got := pctOf(50, 100); got != 50.0 {
		t.Errorf("expected 50.0, got %f", got)
	}
	if got := pctOf(0, 0); got != 0.0 {
		t.Errorf("expected 0.0 for zero total, got %f", got)
	}
}

func TestFeeChart_Empty(t *testing.T) {
	html := feeChart(nil)
	if string(html) != `<div class="chart-empty">No forwarding data yet</div>` {
		t.Errorf("unexpected empty chart output: %s", html)
	}
}

func TestFeeChart_WithData(t *testing.T) {
	data := []db.DailyFeeStat{
		{Day: "2024-01-01", TotalFeeMsat: 5000, Count: 3},
		{Day: "2024-01-02", TotalFeeMsat: 10000, Count: 5},
		{Day: "2024-01-03", TotalFeeMsat: 0, Count: 0},
	}
	html := feeChart(data)
	s := string(html)

	if len(s) == 0 {
		t.Fatal("expected non-empty SVG")
	}
	if !contains(s, "<svg") {
		t.Error("expected SVG element")
	}
	if !contains(s, "chart-bar") {
		t.Error("expected chart-bar class")
	}
	// Should have 3 bars
	count := countOccurrences(s, "class=\"chart-bar\"")
	if count != 3 {
		t.Errorf("expected 3 bars, found %d", count)
	}
}

func TestFeeChart_SingleDay(t *testing.T) {
	data := []db.DailyFeeStat{
		{Day: "2024-01-01", TotalFeeMsat: 5000, Count: 3},
	}
	html := feeChart(data)
	if !contains(string(html), "<svg") {
		t.Error("expected SVG for single day")
	}
}

func TestSeq(t *testing.T) {
	s := seq(5)
	if len(s) != 5 {
		t.Fatalf("expected 5 elements, got %d", len(s))
	}
	for i, v := range s {
		if v != i {
			t.Errorf("seq[%d] = %d, want %d", i, v, i)
		}
	}
}

// helpers
func contains(s, substr string) bool {
	return len(s) >= len(substr) && countOccurrences(s, substr) > 0
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
