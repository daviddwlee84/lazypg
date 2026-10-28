package config

import "bytes"

// EnvLocal is the implicit environment backed by a lazypg-managed container.
const EnvLocal = "local"

// Tiers, from most to least disposable.
const (
	TierLocal = "local"
	TierDev   = "dev"
	TierStage = "stage"
	TierProd  = "prod"
)

// ValidTier reports whether t is a known tier.
func ValidTier(t string) bool { return TierRank(t) >= 0 }

// TierRank orders tiers by blast radius; unknown tiers return -1.
func TierRank(t string) int {
	switch t {
	case TierLocal:
		return 0
	case TierDev:
		return 1
	case TierStage:
		return 2
	case TierProd:
		return 3
	}
	return -1
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
