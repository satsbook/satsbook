package license

import "testing"

func TestTierAtLeast(t *testing.T) {
	tests := []struct {
		current  Tier
		required Tier
		want     bool
	}{
		{TierFree, TierFree, true},
		{TierFree, TierPro, false},
		{TierFree, TierPower, false},
		{TierPro, TierFree, true},
		{TierPro, TierPro, true},
		{TierPro, TierPower, false},
		{TierPower, TierFree, true},
		{TierPower, TierPro, true},
		{TierPower, TierPower, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.current)+"_vs_"+string(tt.required), func(t *testing.T) {
			got := TierAtLeast(tt.current, tt.required)
			if got != tt.want {
				t.Errorf("TierAtLeast(%q, %q) = %v, want %v", tt.current, tt.required, got, tt.want)
			}
		})
	}
}

func TestValidTier(t *testing.T) {
	tests := []struct {
		tier string
		want bool
	}{
		{"free", true},
		{"pro", true},
		{"power", true},
		{"", false},
		{"premium", false},
		{"enterprise", false},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			if got := ValidTier(tt.tier); got != tt.want {
				t.Errorf("ValidTier(%q) = %v, want %v", tt.tier, got, tt.want)
			}
		})
	}
}

func TestFeatureAllowed(t *testing.T) {
	tests := []struct {
		tier    Tier
		feature string
		want    bool
	}{
		// Pro features
		{TierFree, "tax_export", false},
		{TierPro, "tax_export", true},
		{TierPower, "tax_export", true},
		{TierFree, "api_imports", false},
		{TierPro, "api_imports", true},
		{TierFree, "cloud_sync", false},
		{TierPro, "cloud_sync", true},

		// Power features
		{TierFree, "monarch_sync", false},
		{TierPro, "monarch_sync", false},
		{TierPower, "monarch_sync", true},
		{TierFree, "telegram_alerts", false},
		{TierPro, "telegram_alerts", false},
		{TierPower, "telegram_alerts", true},
		{TierFree, "multi_node", false},
		{TierPro, "multi_node", false},
		{TierPower, "multi_node", true},

		// Unknown features default to allowed
		{TierFree, "unknown_feature", true},
		{TierFree, "", true},
	}

	for _, tt := range tests {
		name := string(tt.tier) + "_" + tt.feature
		t.Run(name, func(t *testing.T) {
			got := FeatureAllowed(tt.tier, tt.feature)
			if got != tt.want {
				t.Errorf("FeatureAllowed(%q, %q) = %v, want %v", tt.tier, tt.feature, got, tt.want)
			}
		})
	}
}
