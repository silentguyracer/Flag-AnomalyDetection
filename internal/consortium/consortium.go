package consortium

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"math"
	"sync"
)

// Cryptographic Consortium Mesh for Cross-Bank Threat Sharing.
// Solves the multi-institution privacy paradox: Banks share threat signals
// (compromised PANs, account takeover hardware tokens) without revealing raw PII,
// utilizing blind salted HMAC-SHA256 and lock-free probabilistic Bloom filters.

// BlindToken generates a double-salted one-way cryptographic hash of PII (card PAN, device ID, tax ID).
func BlindToken(pii string, consortiumSalt, bankSalt string) string {
	// First round: Bank-level HMAC
	h1 := hmac.New(sha256.New, []byte(bankSalt))
	h1.Write([]byte(pii))
	intermediate := h1.Sum(nil)

	// Second round: Consortium federation HMAC
	h2 := hmac.New(sha256.New, []byte(consortiumSalt))
	h2.Write(intermediate)
	return hex.EncodeToString(h2.Sum(nil))
}

// BloomFilter is a thread-safe probabilistic data structure for zero-PII cross-bank membership testing.
type BloomFilter struct {
	mu        sync.RWMutex
	bitset    []uint64
	sizeBits  uint64
	numHashes int
}

// NewBloomFilter creates an optimal Bloom Filter sized for expectedItems and falsePositiveRate.
func NewBloomFilter(expectedItems int, falsePositiveRate float64) *BloomFilter {
	if expectedItems <= 0 {
		expectedItems = 100000
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1.0 {
		falsePositiveRate = 0.001 // 0.1% false positive floor
	}

	// m = - (n * ln(p)) / (ln(2)^2)
	m := -1.0 * (float64(expectedItems) * math.Log(falsePositiveRate)) / (math.Pow(math.Ln2, 2))
	sizeBits := uint64(math.Ceil(m))
	// k = (m/n) * ln(2)
	k := int(math.Ceil((float64(sizeBits) / float64(expectedItems)) * math.Ln2))
	if k < 1 {
		k = 1
	}

	numWords := (sizeBits + 63) / 64
	return &BloomFilter{
		bitset:    make([]uint64, numWords),
		sizeBits:  sizeBits,
		numHashes: k,
	}
}

// Add inserts a blind token into the consortium filter.
func (bf *BloomFilter) Add(blindToken string) {
	bf.mu.Lock()
	defer bf.mu.Unlock()

	h1, h2 := hashPair(blindToken)
	for i := 0; i < bf.numHashes; i++ {
		// Double hashing: g_i(x) = (h1(x) + i * h2(x)) mod m
		bit := (h1 + uint64(i)*h2) % bf.sizeBits
		wordIdx := bit / 64
		bitIdx := bit % 64
		bf.bitset[wordIdx] |= (1 << bitIdx)
	}
}

// Contains checks if a blind token exists in the consortium filter with bound false-positive probability.
func (bf *BloomFilter) Contains(blindToken string) bool {
	bf.mu.RLock()
	defer bf.mu.RUnlock()

	h1, h2 := hashPair(blindToken)
	for i := 0; i < bf.numHashes; i++ {
		bit := (h1 + uint64(i)*h2) % bf.sizeBits
		wordIdx := bit / 64
		bitIdx := bit % 64
		if (bf.bitset[wordIdx] & (1 << bitIdx)) == 0 {
			return false // Definitive negative: element definitely not in consortium
		}
	}
	return true // Probabilistic positive: match found in consortium
}

func hashPair(s string) (uint64, uint64) {
	// FNV-1a 64-bit for h1
	h := fnv.New64a()
	h.Write([]byte(s))
	h1 := h.Sum64()

	// SHA-256 derived for h2
	sha := sha256.Sum256([]byte(s))
	var h2 uint64
	for i := 0; i < 8; i++ {
		h2 = (h2 << 8) | uint64(sha[i])
	}
	if h2 == 0 {
		h2 = 1
	}
	return h1, h2
}

// ConsortiumNetwork maintains inter-bank federated threat lists.
type ConsortiumNetwork struct {
	mu           sync.RWMutex
	salt         string
	compromised  *BloomFilter
	entityTokens map[string]int // Token -> count of reporting banks
}

func NewConsortiumNetwork(federationSalt string) *ConsortiumNetwork {
	if federationSalt == "" {
		federationSalt = "federated-consortium-global-salt-2026"
	}
	return &ConsortiumNetwork{
		salt:         federationSalt,
		compromised:  NewBloomFilter(500000, 0.0001),
		entityTokens: make(map[string]int),
	}
}

// ReportCompromised records a compromised entity token broadcast by a peer bank.
func (cn *ConsortiumNetwork) ReportCompromised(blindToken string) {
	cn.mu.Lock()
	defer cn.mu.Unlock()

	cn.compromised.Add(blindToken)
	cn.entityTokens[blindToken]++
}

// CheckEntity returns whether the blind token is flagged across the consortium mesh.
func (cn *ConsortiumNetwork) CheckEntity(blindToken string) (bool, int) {
	cn.mu.RLock()
	defer cn.mu.RUnlock()

	match := cn.compromised.Contains(blindToken)
	if !match {
		return false, 0
	}
	return true, cn.entityTokens[blindToken]
}

// FederationSalt returns the global consortium salt.
func (cn *ConsortiumNetwork) FederationSalt() string {
	return cn.salt
}
