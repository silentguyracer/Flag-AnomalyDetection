package consortium

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlindTokenDeterminism(t *testing.T) {
	globalSalt := "consortium-salt-xyz"
	bankSalt := "barclays-salt-123"

	token1 := BlindToken("4532-0150-9999-1234", globalSalt, bankSalt)
	token2 := BlindToken("4532-0150-9999-1234", globalSalt, bankSalt)
	token3 := BlindToken("4532-0150-9999-9999", globalSalt, bankSalt)

	assert.Equal(t, token1, token2, "Identical PII and salts must produce identical deterministic blind tokens")
	assert.NotEqual(t, token1, token3, "Different PII must produce different blind tokens")
}

func TestBloomFilter_Membership(t *testing.T) {
	bf := NewBloomFilter(1000, 0.001)

	items := []string{
		"token_alpha_1",
		"token_beta_2",
		"token_gamma_3",
	}

	for _, item := range items {
		bf.Add(item)
	}

	for _, item := range items {
		assert.True(t, bf.Contains(item), "Bloom filter must contain added item: %s", item)
	}

	assert.False(t, bf.Contains("token_never_added_99"), "Bloom filter should not contain unadded item")
}

func TestConsortiumNetwork_FederatedCheck(t *testing.T) {
	mesh := NewConsortiumNetwork("global-consortium-test")

	stolenCardToken := BlindToken("card_compromised_at_atm_88", mesh.FederationSalt(), "bank_hsbc")
	cleanCardToken := BlindToken("card_clean_everyday_user", mesh.FederationSalt(), "bank_monzo")

	// Bank 1 and Bank 2 report stolen card
	mesh.ReportCompromised(stolenCardToken)
	mesh.ReportCompromised(stolenCardToken)

	isFlagged, count := mesh.CheckEntity(stolenCardToken)
	require.True(t, isFlagged, "Stolen card token must match consortium list")
	assert.Equal(t, 2, count, "Must show 2 reporting peer banks")

	cleanFlagged, _ := mesh.CheckEntity(cleanCardToken)
	assert.False(t, cleanFlagged, "Clean card token should not match consortium")
}
