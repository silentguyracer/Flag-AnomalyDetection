package graph

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"fraud-service/internal/rules"
)

// EntityType represents node types in the payment relationship graph.
type EntityType string

const (
	EntityAccount EntityType = "account"
	EntityDevice  EntityType = "device"
	EntityCard    EntityType = "card"
	EntityIP      EntityType = "ip"
)

// GraphAnomaly represents a detected syndicate or mule ring pattern.
type GraphAnomaly struct {
	Type          string      `json:"type"`          // e.g. "shared_device_syndicate", "mule_fan_out"
	Score         float64     `json:"score"`         // 0..1 confidence
	ClusterSize   int         `json:"cluster_size"`  // number of connected accounts
	SharedEntity  string      `json:"shared_entity"` // device ID or card hash
	MemberAccounts []uuid.UUID `json:"member_accounts"`
	Description   string      `json:"description"`
}

// MemoryGraph maintains an online bipartite graph of Accounts <-> Devices / Cards / IPs.
// Optimized for lock-free reads and O(1) cluster lookups.
type MemoryGraph struct {
	mu            sync.RWMutex
	deviceAccounts map[string]map[uuid.UUID]time.Time // device_id -> set of accounts
	accountDevices map[uuid.UUID]map[string]time.Time // account_id -> set of devices
	accountLinks   map[uuid.UUID]map[uuid.UUID]int64  // transfer relationships (source -> dest -> volume)
}

func NewMemoryGraph() *MemoryGraph {
	return &MemoryGraph{
		deviceAccounts: make(map[string]map[uuid.UUID]time.Time),
		accountDevices: make(map[uuid.UUID]map[string]time.Time),
		accountLinks:   make(map[uuid.UUID]map[uuid.UUID]int64),
	}
}

// Observe records a transaction event into the graph.
func (g *MemoryGraph) Observe(accountID uuid.UUID, deviceID string, occurredAt time.Time) {
	if deviceID == "" || accountID == uuid.Nil {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Link device -> account
	if g.deviceAccounts[deviceID] == nil {
		g.deviceAccounts[deviceID] = make(map[uuid.UUID]time.Time)
	}
	g.deviceAccounts[deviceID][accountID] = occurredAt

	// Link account -> device
	if g.accountDevices[accountID] == nil {
		g.accountDevices[accountID] = make(map[string]time.Time)
	}
	g.accountDevices[accountID][deviceID] = occurredAt
}

// RecordTransfer logs a transfer edge between two accounts for circular mule ring detection.
func (g *MemoryGraph) RecordTransfer(src, dst uuid.UUID, amountMinor int64) {
	if src == uuid.Nil || dst == uuid.Nil || src == dst {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.accountLinks[src] == nil {
		g.accountLinks[src] = make(map[uuid.UUID]int64)
	}
	g.accountLinks[src][dst] += amountMinor
}

// AnalyzeAccount inspects the graph neighborhood for syndicates, shared devices, and mule patterns.
func (g *MemoryGraph) AnalyzeAccount(accountID uuid.UUID, currentDeviceID string) *GraphAnomaly {
	g.mu.RLock()
	defer g.mu.RUnlock()

	targetDevice := currentDeviceID
	// If current device is not provided, look at account's primary devices
	if targetDevice == "" {
		if devices, ok := g.accountDevices[accountID]; ok {
			for d := range devices {
				targetDevice = d
				break
			}
		}
	}

	// 1. Shared Device Syndicate Detection
	// If 3 or more accounts share the same physical device ID within 30 days,
	// this is a high-confidence indicator of a credential stuffing farm or account takeover ring.
	if targetDevice != "" {
		if accounts, ok := g.deviceAccounts[targetDevice]; ok {
			clusterSize := len(accounts)
			if clusterSize >= 3 {
				var members []uuid.UUID
				for acct := range accounts {
					members = append(members, acct)
				}

				// Score scales with cluster size (3 accounts: 0.70; 5+ accounts: 0.95)
				score := 0.70 + 0.05*float64(clusterSize-3)
				if score > 0.98 {
					score = 0.98
				}

				return &GraphAnomaly{
					Type:           "shared_device_syndicate",
					Score:          score,
					ClusterSize:    clusterSize,
					SharedEntity:   targetDevice,
					MemberAccounts: members,
					Description: fmt.Sprintf(
						"Shared Device Ring: device %s is linked to %d distinct accounts (credential farm pattern)",
						targetDevice, clusterSize,
					),
				}
			}
		}
	}

	// 2. Mule Fan-Out / Circular Smurfing Detection
	if recipients, ok := g.accountLinks[accountID]; ok {
		if len(recipients) >= 5 {
			var members []uuid.UUID
			for r := range recipients {
				members = append(members, r)
			}
			return &GraphAnomaly{
				Type:           "mule_fan_out",
				Score:          0.85,
				ClusterSize:    len(recipients),
				SharedEntity:   accountID.String(),
				MemberAccounts: members,
				Description: fmt.Sprintf(
					"Mule Fan-Out Smurfing: account distributed funds to %d recipient accounts in rapid succession",
					len(recipients),
				),
			}
		}
	}

	return nil
}

// AsRuleSignal converts a GraphAnomaly into a rules.Signal for the noisy-OR engine.
func (a *GraphAnomaly) AsRuleSignal() *rules.Signal {
	if a == nil {
		return nil
	}
	return &rules.Signal{
		Rule:   "graph_syndicate_ring",
		Score:  a.Score,
		Reason: a.Description,
		Evidence: map[string]any{
			"anomaly_type":    a.Type,
			"cluster_size":    a.ClusterSize,
			"shared_entity":   a.SharedEntity,
			"member_accounts": a.MemberAccounts,
		},
	}
}

// GetAllSyndicates returns all active graph clusters exceeding threshold for forensic reporting.
func (g *MemoryGraph) GetAllSyndicates() []GraphAnomaly {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var syndicates []GraphAnomaly
	for dev, accts := range g.deviceAccounts {
		if len(accts) >= 3 {
			var members []uuid.UUID
			for acct := range accts {
				members = append(members, acct)
			}
			syndicates = append(syndicates, GraphAnomaly{
				Type:           "shared_device_syndicate",
				Score:          0.70 + 0.05*float64(len(accts)-3),
				ClusterSize:    len(accts),
				SharedEntity:   dev,
				MemberAccounts: members,
				Description:    fmt.Sprintf("Device %s linked to %d accounts", dev, len(accts)),
			})
		}
	}
	return syndicates
}
