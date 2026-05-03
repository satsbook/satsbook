package license

// Tier represents a subscription tier level.
type Tier string

const (
	TierFree  Tier = "free"
	TierPro   Tier = "pro"
	TierPower Tier = "power"
)

// tierOrder maps each tier to its rank for comparison.
var tierOrder = map[Tier]int{
	TierFree:  0,
	TierPro:   1,
	TierPower: 2,
}

// TierAtLeast returns true if current tier is at or above the required tier.
func TierAtLeast(current, required Tier) bool {
	return tierOrder[current] >= tierOrder[required]
}

// ValidTier returns true if the tier string is a known tier.
func ValidTier(t string) bool {
	_, ok := tierOrder[Tier(t)]
	return ok
}

// FeatureTiers maps feature names to the minimum tier required.
var FeatureTiers = map[string]Tier{
	"tax_export":      TierPro,
	"api_imports":     TierPro,
	"cloud_sync":      TierPro,
	"monarch_sync":    TierPower,
	"telegram_alerts": TierPower,
	"multi_node":      TierPower,
}

// FeatureAllowed returns true if the given tier can access the named feature.
func FeatureAllowed(tier Tier, feature string) bool {
	required, ok := FeatureTiers[feature]
	if !ok {
		return true // unknown features default to allowed (free)
	}
	return TierAtLeast(tier, required)
}
