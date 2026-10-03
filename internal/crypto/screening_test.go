package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScreenAddress_OFACMixerHit(t *testing.T) {
	// Tornado Cash 1 ETH Pool (OFAC Sanctioned)
	tornadoAddr := "0x47CE0C6eD5B0cE3d3A51Fdb1C52DC66a7c3C2936"

	report := ScreenAddress(tornadoAddr, "ETH")
	require.True(t, report.IsBlocked, "Tornado cash pool must be blocked")
	assert.Equal(t, 1.00, report.RiskScore)
	assert.Equal(t, CategoryMixerTumbler, report.Category)
	assert.Equal(t, "Tornado.Cash 1 ETH Pool", report.EntityIdentified)
	assert.Contains(t, report.Explanation, "MANDATORY REGULATORY BLOCK")
}

func TestScreenAddress_LazarusGroupSanction(t *testing.T) {
	// Lazarus Group Ronin Exploiter
	lazarusAddr := "0x098b716b8aaf21512996dc57eb0615e2383e2f96"

	report := ScreenAddress(lazarusAddr, "ETH")
	assert.True(t, report.IsBlocked)
	assert.Equal(t, CategorySanctionedOFAC, report.Category)
	assert.Equal(t, "Lazarus Group (Ronin Exploiter)", report.EntityIdentified)
}

func TestScreenAddress_CleanAddress(t *testing.T) {
	cleanAddr := "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045" // vitalik.eth

	report := ScreenAddress(cleanAddr, "ETH")
	assert.False(t, report.IsBlocked)
	assert.Equal(t, 0.0, report.RiskScore)
	assert.Equal(t, CategoryClean, report.Category)
}
