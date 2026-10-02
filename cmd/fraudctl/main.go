package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"fraud-service/internal/authgate"
	"fraud-service/internal/bandit"
	"fraud-service/internal/biometrics"
	"fraud-service/internal/canary"
	"fraud-service/internal/consortium"
	"fraud-service/internal/drift"
	"fraud-service/internal/events"
	"fraud-service/internal/features"
	"fraud-service/internal/graph"
	"fraud-service/internal/mcc"
	"fraud-service/internal/rules"
	"fraud-service/internal/sar"
	"fraud-service/internal/xai"
)

type BacktestResult struct {
	Rule           string
	Flags          int
	TruePositives  int
	FalsePositives int
	Precision      float64
	Recall         float64
}

type BacktestSummary struct {
	TotalTxns       int
	TotalFraud      int
	CombinedFlags   int
	CombinedTP      int
	CombinedFP      int
	AlertsPer1k     float64
	MedianDelayTxns int
	RuleMetrics     map[string]*BacktestResult
}

type EvalRecord struct {
	TxnID       uuid.UUID
	AccountID   uuid.UUID
	OccurredAt  time.Time
	AmountMinor int64
	Country     string
	Merchant    string
	Channel     string
	IsFraud     bool
	Scenario    string
}

type StoredTxn struct {
	Txn        events.TransactionCreated
	OccurredAt time.Time
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "backtest":
		runBacktest(os.Args[2:])
	case "compare":
		runCompare(os.Args[2:])
	case "simulate":
		runSimulate(os.Args[2:])
	case "auth-check":
		runAuthCheck(os.Args[2:])
	case "graph-rings":
		runGraphRings(os.Args[2:])
	case "drift-check":
		runDriftCheck(os.Args[2:])
	case "explain":
		runExplain(os.Args[2:])
	case "cycle-detect":
		runCycleDetect(os.Args[2:])
	case "risk-diffusion":
		runRiskDiffusion(os.Args[2:])
	case "canary-status":
		runCanaryStatus(os.Args[2:])
	case "sar":
		runSAR(os.Args[2:])
	case "consortium-query":
		runConsortiumQuery(os.Args[2:])
	case "bandit-tune":
		runBanditTune(os.Args[2:])
	case "biometrics-check":
		runBiometricsCheck(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("fraudctl - Advanced Fraud & Anomaly Engineering CLI")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  fraudctl backtest          --rules <path> [--db <url>] [--samples <n>]")
	fmt.Println("  fraudctl compare           --a <rules_a.yaml> --b <rules_b.yaml> [--samples <n>]")
	fmt.Println("  fraudctl simulate          --scenario <burst|card_testing|account_takeover|impossible_travel>")
	fmt.Println("  fraudctl auth-check        --amount <minor> --country <CC> --channel <type> [--device <id>]")
	fmt.Println("  fraudctl graph-rings       [--accounts <n>]")
	fmt.Println("  fraudctl drift-check       [--samples <n>]")
	fmt.Println("  fraudctl explain           --amount <minor> --country <CC> --channel <type> [--mcc <code>]")
	fmt.Println("  fraudctl cycle-detect      [--depth <n>]")
	fmt.Println("  fraudctl risk-diffusion    [--min-risk <float>]")
	fmt.Println("  fraudctl canary-status     [--a <rules_a.yaml>] [--b <rules_b.yaml>] [--samples <n>]")
	fmt.Println("  fraudctl sar               [--account-id <uuid>] [--amount <minor>]")
	fmt.Println("  fraudctl consortium-query  [--token <string>]")
	fmt.Println("  fraudctl bandit-tune       [--episodes <n>]")
	fmt.Println("  fraudctl biometrics-check  [--synthetic <bool>]")
}

func runBacktest(args []string) {
	fs := flag.NewFlagSet("backtest", flag.ExitOnError)
	rulesPath := fs.String("rules", "configs/rules.yaml", "Path to candidate rules YAML config")
	dbURL := fs.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL database connection string")
	since := fs.String("since", "", "Filter transactions since date (YYYY-MM-DD)")
	sampleCount := fs.Int("samples", 10000, "Number of simulated transactions if DB is not used")
	_ = fs.Parse(args)

	cfg, err := rules.LoadConfig(*rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading rules: %v\n", err)
		os.Exit(1)
	}

	records := loadEvaluationRecords(*dbURL, *since, *sampleCount)
	summary := evaluateRuleset(cfg, records)

	printSummary(cfg.Version, summary)
}

func runCompare(args []string) {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	pathA := fs.String("a", "configs/rules.yaml", "Ruleset A path")
	pathB := fs.String("b", "configs/rules_v4.yaml", "Ruleset B path")
	sampleCount := fs.Int("samples", 15000, "Number of evaluation records")
	_ = fs.Parse(args)

	cfgA, err := rules.LoadConfig(*pathA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading ruleset A: %v\n", err)
		os.Exit(1)
	}
	cfgB, err := rules.LoadConfig(*pathB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading ruleset B: %v\n", err)
		os.Exit(1)
	}

	records := generateSyntheticRecords(*sampleCount, 42)
	sumA := evaluateRuleset(cfgA, records)
	sumB := evaluateRuleset(cfgB, records)

	fmt.Printf("\n=== Ruleset Comparison: %s vs %s ===\n", cfgA.Version, cfgB.Version)
	fmt.Printf("Evaluated on n=%d transactions (%d ground-truth fraud)\n\n", sumA.TotalTxns, sumA.TotalFraud)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "Metric\t%s (A)\t%s (B)\tDelta\n", cfgA.Version, cfgB.Version)
	fmt.Fprintf(w, "------\t------\t------\t-----\n")
	fmt.Fprintf(w, "Total Flags\t%d\t%d\t%+d\n", sumA.CombinedFlags, sumB.CombinedFlags, sumB.CombinedFlags-sumA.CombinedFlags)
	fmt.Fprintf(w, "True Positives (TP)\t%d\t%d\t%+d\n", sumA.CombinedTP, sumB.CombinedTP, sumB.CombinedTP-sumA.CombinedTP)
	fmt.Fprintf(w, "False Positives (FP)\t%d\t%d\t%+d\n", sumA.CombinedFP, sumB.CombinedFP, sumB.CombinedFP-sumA.CombinedFP)
	
	precA := 0.0
	if sumA.CombinedFlags > 0 { precA = float64(sumA.CombinedTP) / float64(sumA.CombinedFlags) }
	precB := 0.0
	if sumB.CombinedFlags > 0 { precB = float64(sumB.CombinedTP) / float64(sumB.CombinedFlags) }
	fmt.Fprintf(w, "Precision\t%.2f\t%.2f\t%+.2f\n", precA, precB, precB-precA)

	recA := 0.0
	if sumA.TotalFraud > 0 { recA = float64(sumA.CombinedTP) / float64(sumA.TotalFraud) }
	recB := 0.0
	if sumB.TotalFraud > 0 { recB = float64(sumB.CombinedTP) / float64(sumB.TotalFraud) }
	fmt.Fprintf(w, "Recall\t%.2f\t%.2f\t%+.2f\n", recA, recB, recB-recA)
	fmt.Fprintf(w, "Alerts / 1k Txns\t%.1f\t%.1f\t%+.1f\n", sumA.AlertsPer1k, sumB.AlertsPer1k, sumB.AlertsPer1k-sumA.AlertsPer1k)
	w.Flush()
	fmt.Println("")
}

func runSimulate(args []string) {
	fs := flag.NewFlagSet("simulate", flag.ExitOnError)
	scenario := fs.String("scenario", "card_testing", "Scenario to simulate: burst, card_testing, account_takeover, impossible_travel")
	accountIDStr := fs.String("account", "", "Target account UUID (optional)")
	rulesPath := fs.String("rules", "configs/rules.yaml", "Rules config path")
	_ = fs.Parse(args)

	cfg, err := rules.LoadConfig(*rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading rules: %v\n", err)
		os.Exit(1)
	}

	var accountID uuid.UUID
	if *accountIDStr != "" {
		accountID, _ = uuid.Parse(*accountIDStr)
	}
	if accountID == uuid.Nil {
		accountID = uuid.New()
	}

	engine := rules.NewEngine(cfg)
	fmt.Printf("\n--- Simulating scenario '%s' for account %s ---\n\n", *scenario, accountID)

	txns := generateScenarioTxns(*scenario, accountID)
	type StoredTxn struct {
		Txn        events.TransactionCreated
		OccurredAt time.Time
	}

	simHistory := make([]StoredTxn, 0)
	profile := &features.WelfordProfile{}
	firstSeen := time.Now().Add(-30 * 24 * time.Hour)
	knownCountries := map[string]time.Time{"GB": firstSeen}
	var lastCountry string
	var lastTxnAt time.Time

	for i, item := range txns {
		t := item.Txn
		now := item.OccurredAt
		// compute features at event time
		count1m := 0
		count10m := 0
		smallCount10m := 0
		var sum1h int64
		merchantSeen := false

		for _, prev := range simHistory {
			if prev.OccurredAt.After(now.Add(-1 * time.Minute)) && !prev.OccurredAt.After(now) {
				count1m++
			}
			if prev.OccurredAt.After(now.Add(-10 * time.Minute)) && !prev.OccurredAt.After(now) {
				count10m++
				if prev.Txn.AmountMinor < 200 {
					smallCount10m++
				}
			}
			if prev.OccurredAt.After(now.Add(-1 * time.Hour)) && !prev.OccurredAt.After(now) {
				sum1h += prev.Txn.AmountMinor
			}
			if prev.Txn.Merchant == t.Merchant {
				merchantSeen = true
			}
		}

		feat := &rules.Features{
			Txn:                 t,
			At:                  now,
			Count1m:             count1m,
			Count10m:            count10m,
			Sum1h:               sum1h,
			ProfileN:            profile.N,
			MeanLog:             profile.MeanLog,
			StdLog:              profile.StdLog(),
			KnownCountries:      knownCountries,
			LastCountry:         lastCountry,
			LastTxnAt:           lastTxnAt,
			AccountAge:          now.Sub(firstSeen),
			MerchantSeen:        merchantSeen,
			RecentSmallCount10m: smallCount10m,
		}

		signals := engine.Run(feat)
		score := rules.Combine(signals)
		flagged := score >= cfg.FlagThreshold

		flagStr := " [PASS] "
		if flagged {
			flagStr = " [FLAG] "
		}

		fmt.Printf("Txn #%d: £%.2f at %s (%s) | score: %.2f%s\n",
			i+1, float64(t.AmountMinor)/100.0, t.Merchant, t.Country, score, flagStr)

		for _, s := range signals {
			fmt.Printf("   └── Signal: %s (score=%.2f) - %s\n", s.Rule, s.Score, s.Reason)
		}

		// update profile & history AFTER evaluation
		profile.Update(t.AmountMinor)
		simHistory = append(simHistory, StoredTxn{Txn: t, OccurredAt: now})
		if t.Country != "" {
			knownCountries[t.Country] = now
			lastCountry = t.Country
		}
		lastTxnAt = now
	}
	fmt.Println("")
}

func evaluateRuleset(cfg *rules.Config, records []EvalRecord) *BacktestSummary {
	engine := rules.NewEngine(cfg)
	summary := &BacktestSummary{
		TotalTxns:   len(records),
		RuleMetrics: make(map[string]*BacktestResult),
	}

	ruleNames := []string{"velocity", "card_testing", "amount_outlier", "new_country", "impossible_travel", "new_merchant_high_value"}
	for _, name := range ruleNames {
		summary.RuleMetrics[name] = &BacktestResult{Rule: name}
	}

	// Group transactions by account and sort by occurred_at
	byAccount := make(map[uuid.UUID][]EvalRecord)
	for _, r := range records {
		if r.IsFraud {
			summary.TotalFraud++
		}
		byAccount[r.AccountID] = append(byAccount[r.AccountID], r)
	}

	for _, list := range byAccount {
		sort.Slice(list, func(i, j int) bool {
			return list[i].OccurredAt.Before(list[j].OccurredAt)
		})
	}

	delays := []int{}

	for _, list := range byAccount {
		var history []StoredTxn
		profile := &features.WelfordProfile{}
		knownCountries := make(map[string]time.Time)
		var lastCountry string
		var lastTxnAt time.Time
		firstSeen := list[0].OccurredAt.Add(-30 * 24 * time.Hour)
		knownCountries["GB"] = firstSeen

		consecutiveFraudIdx := 0

		for _, rec := range list {
			t := events.TransactionCreated{
				TransactionID: rec.TxnID,
				AccountID:     rec.AccountID,
				AmountMinor:   rec.AmountMinor,
				Currency:      "GBP",
				Merchant:      rec.Merchant,
				Country:       rec.Country,
				Channel:       rec.Channel,
			}

			// Compute features before recording
			count1m := 0
			count10m := 0
			smallCount10m := 0
			var sum1h int64
			merchantSeen := false

			for _, prev := range history {
				if prev.OccurredAt.After(rec.OccurredAt.Add(-1 * time.Minute)) && !prev.OccurredAt.After(rec.OccurredAt) {
					count1m++
				}
				if prev.OccurredAt.After(rec.OccurredAt.Add(-10 * time.Minute)) && !prev.OccurredAt.After(rec.OccurredAt) {
					count10m++
					if prev.Txn.AmountMinor < 200 {
						smallCount10m++
					}
				}
				if prev.OccurredAt.After(rec.OccurredAt.Add(-1 * time.Hour)) && !prev.OccurredAt.After(rec.OccurredAt) {
					sum1h += prev.Txn.AmountMinor
				}
				if prev.Txn.Merchant == t.Merchant {
					merchantSeen = true
				}
			}

			feat := &rules.Features{
				Txn:                 t,
				At:                  rec.OccurredAt,
				Count1m:             count1m,
				Count10m:            count10m,
				Sum1h:               sum1h,
				ProfileN:            profile.N,
				MeanLog:             profile.MeanLog,
				StdLog:              profile.StdLog(),
				KnownCountries:      knownCountries,
				LastCountry:         lastCountry,
				LastTxnAt:           lastTxnAt,
				AccountAge:          rec.OccurredAt.Sub(firstSeen),
				MerchantSeen:        merchantSeen,
				RecentSmallCount10m: smallCount10m,
			}

			signals := engine.Run(feat)
			score := rules.Combine(signals)

			// Record individual rule trigger metrics
			for _, sig := range signals {
				rm, ok := summary.RuleMetrics[sig.Rule]
				if !ok {
					rm = &BacktestResult{Rule: sig.Rule}
					summary.RuleMetrics[sig.Rule] = rm
				}
				rm.Flags++
				if rec.IsFraud {
					rm.TruePositives++
				} else {
					rm.FalsePositives++
				}
			}

			// Combined evaluation
			if score >= cfg.FlagThreshold {
				summary.CombinedFlags++
				if rec.IsFraud {
					summary.CombinedTP++
					if consecutiveFraudIdx > 0 {
						delays = append(delays, consecutiveFraudIdx)
					} else {
						delays = append(delays, 1)
					}
				} else {
					summary.CombinedFP++
				}
			}

			if rec.IsFraud {
				consecutiveFraudIdx++
			} else {
				consecutiveFraudIdx = 0
			}

			// Update state AFTER evaluation
			profile.Update(t.AmountMinor)
			history = append(history, StoredTxn{
				Txn:        t,
				OccurredAt: rec.OccurredAt,
			})
			if rec.Country != "" {
				knownCountries[rec.Country] = rec.OccurredAt
				lastCountry = rec.Country
			}
			lastTxnAt = rec.OccurredAt
		}
	}

	// Calculate precision & recall
	for _, rm := range summary.RuleMetrics {
		if rm.Flags > 0 {
			rm.Precision = float64(rm.TruePositives) / float64(rm.Flags)
		}
		if summary.TotalFraud > 0 {
			rm.Recall = float64(rm.TruePositives) / float64(summary.TotalFraud)
		}
	}

	if summary.TotalTxns > 0 {
		summary.AlertsPer1k = float64(summary.CombinedFlags) / float64(summary.TotalTxns) * 1000.0
	}

	if len(delays) > 0 {
		sort.Ints(delays)
		summary.MedianDelayTxns = delays[len(delays)/2]
	} else {
		summary.MedianDelayTxns = 1
	}

	return summary
}

func printSummary(version string, s *BacktestSummary) {
	fmt.Printf("\n=== rules %s vs labelled data (n=%d txns, %d fraud) ===\n\n",
		version, s.TotalTxns, s.TotalFraud)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "rule\tflags\tTP\tFP\tprecision\trecall\n")
	fmt.Fprintf(w, "----\t-----\t--\t--\t---------\t------\n")

	orderedRules := []string{"velocity", "card_testing", "amount_outlier", "new_country", "impossible_travel", "new_merchant_high_value"}
	for _, rName := range orderedRules {
		rm, ok := s.RuleMetrics[rName]
		if !ok || rm.Flags == 0 {
			continue
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%.2f\t%.2f\n",
			rm.Rule, rm.Flags, rm.TruePositives, rm.FalsePositives, rm.Precision, rm.Recall)
	}

	combinedPrec := 0.0
	if s.CombinedFlags > 0 {
		combinedPrec = float64(s.CombinedTP) / float64(s.CombinedFlags)
	}
	combinedRec := 0.0
	if s.TotalFraud > 0 {
		combinedRec = float64(s.CombinedTP) / float64(s.TotalFraud)
	}

	fmt.Fprintf(w, "combined\t%d\t%d\t%d\t%.2f\t%.2f\n",
		s.CombinedFlags, s.CombinedTP, s.CombinedFP, combinedPrec, combinedRec)
	w.Flush()

	fmt.Printf("\nalerts per 1,000 txns: %.1f   median detection delay: %d txns\n\n",
		s.AlertsPer1k, s.MedianDelayTxns)
}

func loadEvaluationRecords(dbURL, since string, sampleCount int) []EvalRecord {
	if dbURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dbURL)
		if err == nil {
			defer pool.Close()
			query := `
				SELECT h.transaction_id, h.account_id, h.occurred_at, h.amount_minor,
				       COALESCE(h.country, ''), h.merchant, COALESCE(h.channel, ''),
				       COALESCE(fl.label, '')
				FROM txn_history h
				LEFT JOIN feature_log fl ON h.transaction_id = fl.transaction_id
				ORDER BY h.occurred_at ASC
			`
			rows, qerr := pool.Query(ctx, query)
			if qerr == nil {
				defer rows.Close()
				var records []EvalRecord
				for rows.Next() {
					var r EvalRecord
					var label string
					if err := rows.Scan(&r.TxnID, &r.AccountID, &r.OccurredAt, &r.AmountMinor, &r.Country, &r.Merchant, &r.Channel, &label); err == nil {
						r.IsFraud = (label == "fraud")
						records = append(records, r)
					}
				}
				if len(records) > 0 {
					return records
				}
			}
		}
	}

	return generateSyntheticRecords(sampleCount, 42)
}

func generateSyntheticRecords(n int, seed int64) []EvalRecord {
	rng := rand.New(rand.NewSource(seed))
	var records []EvalRecord

	numAccounts := 100
	accountIDs := make([]uuid.UUID, numAccounts)
	accountMeans := make([]float64, numAccounts)
	for i := 0; i < numAccounts; i++ {
		accountIDs[i] = uuid.New()
		accountMeans[i] = math.Log(float64(rng.Intn(3000) + 1000)) // £10 to £40 avg
	}

	merchants := []string{"Tesco", "Sainsburys", "Amazon", "Costa", "Uber", "Deliveroo", "Shell", "Marks & Spencer"}
	baseTime := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

	// 1. Generate legitimate baseline transactions
	for i := 0; i < n; i++ {
		acctIdx := rng.Intn(numAccounts)
		acctID := accountIDs[acctIdx]
		meanLog := accountMeans[acctIdx]
		
		// lognormal amount around mean
		amtFloat := math.Exp(meanLog + rng.NormFloat64()*0.4)
		amt := int64(math.Max(250, amtFloat))

		recTime := baseTime.Add(time.Duration(i*3) * time.Minute)
		merch := merchants[rng.Intn(len(merchants))]

		records = append(records, EvalRecord{
			TxnID:       uuid.New(),
			AccountID:   acctID,
			OccurredAt:  recTime,
			AmountMinor: amt,
			Country:     "GB",
			Merchant:    merch,
			Channel:     "contactless",
			IsFraud:     false,
		})
	}

	// 2. Inject labeled attack scenarios:
	// A) Card-testing attack on account 5
	cardTestingAcct := accountIDs[5]
	ctBaseTime := baseTime.Add(200 * time.Minute)
	for j := 0; j < 4; j++ {
		records = append(records, EvalRecord{
			TxnID:       uuid.New(),
			AccountID:   cardTestingAcct,
			OccurredAt:  ctBaseTime.Add(time.Duration(j*45) * time.Second),
			AmountMinor: 100, // £1.00 micro-charge
			Country:     "GB",
			Merchant:    "Online-Shop-Auth",
			Channel:     "online",
			IsFraud:     true,
			Scenario:    "card_testing",
		})
	}
	records = append(records, EvalRecord{
		TxnID:       uuid.New(),
		AccountID:   cardTestingAcct,
		OccurredAt:  ctBaseTime.Add(4 * time.Minute),
		AmountMinor: 9500, // £95.00 cashout
		Country:     "GB",
		Merchant:    "HighEndTech Store",
		Channel:     "online",
		IsFraud:     true,
		Scenario:    "card_testing",
	})

	// B) Velocity Burst on account 12 (8 transactions in 40 seconds)
	burstAcct := accountIDs[12]
	burstBaseTime := baseTime.Add(500 * time.Minute)
	for j := 0; j < 8; j++ {
		records = append(records, EvalRecord{
			TxnID:       uuid.New(),
			AccountID:   burstAcct,
			OccurredAt:  burstBaseTime.Add(time.Duration(j*5) * time.Second),
			AmountMinor: 4500, // £45.00
			Country:     "GB",
			Merchant:    "CryptoTopup",
			Channel:     "online",
			IsFraud:     true,
			Scenario:    "velocity_burst",
		})
	}

	// C) Account takeover: new country + high value online amount on account 20
	atoAcct := accountIDs[20]
	atoTime := baseTime.Add(800 * time.Minute)
	records = append(records, EvalRecord{
		TxnID:       uuid.New(),
		AccountID:   atoAcct,
		OccurredAt:  atoTime,
		AmountMinor: 48000, // £480.00
		Country:     "JP",
		Merchant:    "ElectroMart Tokyo",
		Channel:     "online",
		IsFraud:     true,
		Scenario:    "account_takeover",
	})

	// D) Impossible travel on account 30 (GB then US 20m later)
	itAcct := accountIDs[30]
	itTime := baseTime.Add(1100 * time.Minute)
	records = append(records, EvalRecord{
		TxnID:       uuid.New(),
		AccountID:   itAcct,
		OccurredAt:  itTime,
		AmountMinor: 3500,
		Country:     "GB",
		Merchant:    "Pret London",
		Channel:     "contactless",
		IsFraud:     false,
	})
	records = append(records, EvalRecord{
		TxnID:       uuid.New(),
		AccountID:   itAcct,
		OccurredAt:  itTime.Add(20 * time.Minute),
		AmountMinor: 12000,
		Country:     "US",
		Merchant:    "BestBuy New York",
		Channel:     "chip",
		IsFraud:     true,
		Scenario:    "impossible_travel",
	})

	// Sort globally by occurred_at
	sort.Slice(records, func(i, j int) bool {
		return records[i].OccurredAt.Before(records[j].OccurredAt)
	})

	return records
}

func generateScenarioTxns(scenario string, acctID uuid.UUID) []StoredTxn {
	now := time.Now().UTC()
	var rawList []events.TransactionCreated

	// 10 legitimate warm-up transactions for baseline
	for i := 0; i < 15; i++ {
		rawList = append(rawList, events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acctID,
			AmountMinor:   int64(1200 + i*50), // ~£12 - £18
			Currency:      "GBP",
			Merchant:      "Tesco Metro",
			Country:       "GB",
			Channel:       "contactless",
		})
		rawList[len(rawList)-1].AmountMinor = 1200
	}

	switch scenario {
	case "card_testing":
		for i := 0; i < 4; i++ {
			rawList = append(rawList, events.TransactionCreated{
				TransactionID: uuid.New(),
				AccountID:     acctID,
				AmountMinor:   150, // £1.50
				Currency:      "GBP",
				Merchant:      "Digital-Goods-Micro",
				Country:       "GB",
				Channel:       "online",
			})
		}
		rawList = append(rawList, events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acctID,
			AmountMinor:   9000, // £90.00
			Currency:      "GBP",
			Merchant:      "CameraStoreUK",
			Country:       "GB",
			Channel:       "online",
		})

	case "burst", "velocity":
		for i := 0; i < 8; i++ {
			rawList = append(rawList, events.TransactionCreated{
				TransactionID: uuid.New(),
				AccountID:     acctID,
				AmountMinor:   3000,
				Currency:      "GBP",
				Merchant:      "QuickPay",
				Country:       "GB",
				Channel:       "contactless",
			})
		}

	case "account_takeover":
		rawList = append(rawList, events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acctID,
			AmountMinor:   55000, // £550.00
			Currency:      "GBP",
			Merchant:      "TokyoLuxury",
			Country:       "JP",
			Channel:       "online",
		})

	case "impossible_travel":
		rawList = append(rawList, events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acctID,
			AmountMinor:   2500,
			Currency:      "GBP",
			Merchant:      "London Cafe",
			Country:       "GB",
			Channel:       "contactless",
		})
		rawList = append(rawList, events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acctID,
			AmountMinor:   45000,
			Currency:      "USD",
			Merchant:      "Miami Electronics",
			Country:       "US",
			Channel:       "chip",
		})
	}

	var result []StoredTxn
	t0 := now.Add(-time.Duration(len(rawList)) * 2 * time.Minute)
	for i, t := range rawList {
		if scenario == "burst" && i >= 15 {
			t.AmountMinor = 3000
			t0 = t0.Add(5 * time.Second)
		} else if scenario == "impossible_travel" && i == len(rawList)-1 {
			t0 = t0.Add(20 * time.Minute)
		} else {
			t0 = t0.Add(1 * time.Minute)
		}
		result = append(result, StoredTxn{
			Txn:        t,
			OccurredAt: t0,
		})
	}

	return result
}

func runAuthCheck(args []string) {
	fs := flag.NewFlagSet("auth-check", flag.ExitOnError)
	amtMinor := fs.Int64("amount", 2500, "Transaction amount in minor units")
	country := fs.String("country", "GB", "Country ISO code (e.g. GB, JP, US)")
	channel := fs.String("channel", "contactless", "Payment channel (contactless, chip, online, atm)")
	merchant := fs.String("merchant", "Tesco Express", "Merchant name")
	deviceID := fs.String("device", "", "Device ID fingerprint")
	rulesPath := fs.String("rules", "configs/rules.yaml", "Rules config path")
	_ = fs.Parse(args)

	cfg, err := rules.LoadConfig(*rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading rules: %v\n", err)
		os.Exit(1)
	}

	engine := rules.NewEngine(cfg)
	memGraph := graph.NewMemoryGraph()
	gate := authgate.NewGate(engine, memGraph, nil, 25*time.Millisecond, true)

	acctID := uuid.New()
	req := authgate.AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     acctID,
		AmountMinor:   *amtMinor,
		Currency:      "GBP",
		Merchant:      *merchant,
		Country:       *country,
		Channel:       *channel,
		DeviceID:      *deviceID,
	}

	// Create baseline profile context
	now := time.Now().UTC()
	feat := &rules.Features{
		Txn: events.TransactionCreated{
			TransactionID: req.TransactionID,
			AccountID:     acctID,
			AmountMinor:   *amtMinor,
			Currency:      "GBP",
			Merchant:      *merchant,
			Country:       *country,
			Channel:       *channel,
			DeviceID:      *deviceID,
		},
		At:             now,
		ProfileN:       20,
		MeanLog:        math.Log(1500), // ~£15 typical spend
		StdLog:         0.35,
		KnownCountries: map[string]time.Time{"GB": now.Add(-30 * 24 * time.Hour)},
		LastCountry:    "GB",
		LastTxnAt:      now.Add(-4 * time.Hour),
		AccountAge:     30 * 24 * time.Hour,
	}

	// If country differs and gap is short, simulate travel
	if *country != "GB" && *country != "" {
		feat.LastTxnAt = now.Add(-15 * time.Minute)
	}

	resp := gate.Evaluate(context.Background(), req, feat)

	fmt.Println("\n=======================================================")
	fmt.Println("  Synchronous Pre-Authorization Risk Gate Evaluation   ")
	fmt.Println("=======================================================")
	fmt.Printf("Payment:       £%.2f at %s (%s, %s)\n", float64(*amtMinor)/100.0, *merchant, *country, *channel)
	fmt.Printf("Decision:      [%s]\n", resp.Decision)
	fmt.Printf("Risk Score:    %.2f (Severity: %s)\n", resp.RiskScore, resp.Severity)
	fmt.Printf("Latency:       %.2f ms (SLA Budget: 25.00 ms)\n", resp.EvaluationMs)

	if len(resp.Reasons) > 0 {
		fmt.Println("\nTriggered Risk Signals:")
		for _, r := range resp.Reasons {
			fmt.Printf("  • %s\n", r)
		}
	} else {
		fmt.Println("\nNo anomalies detected. Immediate authorization approved.")
	}
	fmt.Println("=======================================================")
}

func runGraphRings(args []string) {
	fs := flag.NewFlagSet("graph-rings", flag.ExitOnError)
	numAccounts := fs.Int("accounts", 20, "Number of accounts to simulate into graph")
	_ = fs.Parse(args)

	memGraph := graph.NewMemoryGraph()
	now := time.Now().UTC()

	// Simulate a shared device syndicate: device_syndicate_007 shared across 4 accounts
	syndicateDevice := "device_syndicate_007"
	for i := 0; i < 4; i++ {
		acct := uuid.New()
		memGraph.Observe(acct, syndicateDevice, now.Add(time.Duration(i*5)*time.Minute))
	}

	// Simulate a normal benign device with 1 account
	memGraph.Observe(uuid.New(), "benign_phone_12", now)

	// Simulate a second syndicate: device_atm_skimmer_99 shared across 5 accounts
	skimmerDevice := "device_atm_skimmer_99"
	for i := 0; i < 5; i++ {
		acct := uuid.New()
		memGraph.Observe(acct, skimmerDevice, now.Add(time.Duration(i*2)*time.Minute))
	}

	syndicates := memGraph.GetAllSyndicates()

	fmt.Printf("\n=== Real-Time Entity Graph & Mule Syndicate Scan (n=%d accounts) ===\n\n", *numAccounts)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "Syndicate Type\tShared Entity\tCluster Size\tRisk Score\tStatus\n")
	fmt.Fprintf(w, "--------------\t-------------\t------------\t----------\t------\n")

	for _, s := range syndicates {
		fmt.Fprintf(w, "%s\t%s\t%d accounts\t%.2f\tCONFIRMED CLUSTER\n",
			s.Type, s.SharedEntity, s.ClusterSize, s.Score)
	}
	w.Flush()
	fmt.Printf("\nTotal Syndicates Detected: %d\n\n", len(syndicates))
}

func runDriftCheck(args []string) {
	fs := flag.NewFlagSet("drift-check", flag.ExitOnError)
	samples := fs.Int("samples", 1000, "Number of samples for distribution analysis")
	_ = fs.Parse(args)

	rng := rand.New(rand.NewSource(42))

	// Baseline distribution (lognormal spend mean £15)
	baseline := make([]float64, *samples)
	for i := 0; i < *samples; i++ {
		baseline[i] = math.Exp(math.Log(1500) + rng.NormFloat64()*0.40)
	}

	// Case 1: Stable serving distribution
	servingStable := make([]float64, *samples)
	for i := 0; i < *samples; i++ {
		servingStable[i] = math.Exp(math.Log(1500) + rng.NormFloat64()*0.41)
	}

	// Case 2: Drifting serving distribution (e.g. sudden high-ticket surge / inflation drift)
	servingDrifted := make([]float64, *samples)
	for i := 0; i < *samples; i++ {
		servingDrifted[i] = math.Exp(math.Log(3800) + rng.NormFloat64()*0.55)
	}

	repStable := drift.ComputePSI("transaction_amount (stable)", baseline, servingStable, 10)
	repDrifted := drift.ComputePSI("transaction_amount (shifted)", baseline, servingDrifted, 10)

	fmt.Println("\n=======================================================")
	fmt.Println("  Population Stability Index (PSI) Concept Drift Check ")
	fmt.Println("=======================================================")
	fmt.Printf("Window 1 (Normal Operations): PSI = %.4f | Status: %s\n", repStable.PSI, repStable.Status)
	fmt.Printf("Details: %s\n\n", repStable.Message)

	fmt.Printf("Window 2 (Black Friday Surge): PSI = %.4f | Status: %s\n", repDrifted.PSI, repDrifted.Status)
	fmt.Printf("Details: %s\n", repDrifted.Message)
	fmt.Println("=======================================================")
}

func runExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	amountMinor := fs.Int64("amount", 250000, "Amount in minor units (£2500.00 = 250000)")
	country := fs.String("country", "RU", "Country ISO alpha-2")
	channel := fs.String("channel", "online", "Payment channel (contactless|chip|online|atm)")
	merchant := fs.String("merchant", "Tokyo Luxury Direct", "Merchant descriptor")
	mccCode := fs.String("mcc", "6051", "Merchant Category Code (e.g. 6051 for Crypto, 7995 for Gambling)")
	_ = fs.Parse(args)

	cfg, _ := rules.LoadConfig("configs/rules.yaml")
	if cfg == nil {
		cfg = &rules.Config{FlagThreshold: 0.50}
	}
	engine := rules.NewEngine(cfg)
	memGraph := graph.NewMemoryGraph()
	gate := authgate.NewGate(engine, memGraph, nil, 25*time.Millisecond, false)

	req := authgate.AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     uuid.New(),
		AmountMinor:   *amountMinor,
		Currency:      "GBP",
		Country:       *country,
		Channel:       *channel,
		Merchant:      *merchant,
		MCC:           *mccCode,
	}

	now := time.Now().UTC()
	feat := &rules.Features{
		Txn: events.TransactionCreated{
			TransactionID: req.TransactionID,
			AccountID:     req.AccountID,
			AmountMinor:   *amountMinor,
			Currency:      "GBP",
			Country:       *country,
			Channel:       *channel,
			Merchant:      *merchant,
		},
		At:             now,
		ProfileN:       20,
		MeanLog:        math.Log(1500), // £15 typical spend
		StdLog:         0.35,
		KnownCountries: map[string]time.Time{"GB": now.Add(-30 * 24 * time.Hour)},
		LastCountry:    "GB",
		LastTxnAt:      now.Add(-15 * time.Minute),
		AccountAge:     30 * 24 * time.Hour,
	}

	resp := gate.Evaluate(context.Background(), req, feat)
	adjustedScore, mccProf := mcc.AdjustScore(resp.RiskScore, *mccCode)

	var signals []rules.Signal
	for _, rsn := range resp.Reasons {
		ruleName := "risk_signal"
		if strings.Contains(rsn, "outlier") {
			ruleName = "amount_outlier"
		} else if strings.Contains(rsn, "travel") {
			ruleName = "impossible_travel"
		} else if strings.Contains(rsn, "First-ever payment in") {
			ruleName = "new_country"
		} else if strings.Contains(rsn, "typical spend") {
			ruleName = "new_merchant_high_value"
		}
		signals = append(signals, rules.Signal{
			Rule:   ruleName,
			Score:  resp.RiskScore,
			Reason: rsn,
		})
	}
	if mccProf.RiskMultiplier > 1.0 {
		signals = append(signals, rules.Signal{
			Rule:   "mcc_risk_multiplier",
			Score:  adjustedScore,
			Reason: fmt.Sprintf("High-Risk Merchant Category (%s, %s): %.2fx risk scaling", mccProf.CategoryName, mccProf.Tier, mccProf.RiskMultiplier),
		})
	}

	notice := xai.Explain(string(resp.Decision), adjustedScore, signals)
	fmt.Print(notice.FormatMemorandum())
}

func runCycleDetect(args []string) {
	fs := flag.NewFlagSet("cycle-detect", flag.ExitOnError)
	depth := fs.Int("depth", 5, "Maximum cycle search depth")
	_ = fs.Parse(args)

	memGraph := graph.NewMemoryGraph()

	// Simulate a 4-hop circular laundering ring:
	// A (Layering Root) -> B -> C -> D -> A
	acctA := uuid.New()
	acctB := uuid.New()
	acctC := uuid.New()
	acctD := uuid.New()

	memGraph.RecordTransfer(acctA, acctB, 100000) // £1000.00
	memGraph.RecordTransfer(acctB, acctC, 98000)  // £980.00
	memGraph.RecordTransfer(acctC, acctD, 96000)  // £960.00
	memGraph.RecordTransfer(acctD, acctA, 94000)  // £940.00 (Loop closed)

	// Benign linear transfer: E -> F (no loop)
	acctE := uuid.New()
	acctF := uuid.New()
	memGraph.RecordTransfer(acctE, acctF, 25000)

	cycles := memGraph.DetectCycles(*depth)

	fmt.Println("\n=======================================================")
	fmt.Println("  Circular Money Laundering & Layering Loop Detection ")
	fmt.Println("=======================================================")
	fmt.Printf("Directed Ring Search Depth: %d hops | Active Accounts: 6\n\n", *depth)

	if len(cycles) == 0 {
		fmt.Println("No circular transfer loops detected.")
	} else {
		for i, c := range cycles {
			fmt.Printf("Ring #%d: [%s]\n", i+1, c.RingID)
			fmt.Printf("  • Hop Count:    %d accounts in closed cycle\n", c.HopCount)
			fmt.Printf("  • Total Volume: £%.2f laundered through loop\n", float64(c.TotalVolume)/100.0)
			fmt.Printf("  • Confidence:   %.2f / 1.00\n", c.Confidence)
			fmt.Printf("  • Cycle Path:   ")
			for j, a := range c.Accounts {
				fmt.Printf("%s", a.String()[:8])
				if j < len(c.Accounts)-1 {
					fmt.Printf(" -> ")
				}
			}
			fmt.Printf(" -> %s (CLOSED)\n\n", c.Accounts[0].String()[:8])
		}
	}
	fmt.Println("=======================================================")
}

func runRiskDiffusion(args []string) {
	fs := flag.NewFlagSet("risk-diffusion", flag.ExitOnError)
	minRisk := fs.Float64("min-risk", 0.15, "Minimum contamination risk threshold")
	_ = fs.Parse(args)

	memGraph := graph.NewMemoryGraph()

	seedFraud := uuid.New()
	muleL1 := uuid.New()
	muleL2 := uuid.New()
	merchant := uuid.New()

	// Seed sends 80% to Mule L1, 20% to legit merchant
	memGraph.RecordTransfer(seedFraud, muleL1, 80000)
	memGraph.RecordTransfer(seedFraud, merchant, 20000)

	// Mule L1 passes funds to Mule L2
	memGraph.RecordTransfer(muleL1, muleL2, 75000)

	contaminated := memGraph.GetContaminatedAccounts([]uuid.UUID{seedFraud}, *minRisk)

	fmt.Println("\n=======================================================")
	fmt.Println("  Personalized PageRank Dirty Money Risk Diffusion     ")
	fmt.Println("=======================================================")
	fmt.Printf("Seed Fraud Account: %s\n\n", seedFraud)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Account ID\tRole / Topology\tContamination Score\tStatus")
	fmt.Fprintln(w, "----------\t---------------\t-------------------\t------")

	for _, c := range contaminated {
		role := "Downstream Mule"
		status := "SUSPICIOUS COUNTERPARTY"
		if c.AccountID == seedFraud {
			role = "Confirmed Fraud Seed"
			status = "CONFIRMED FRAUD"
		} else if c.AccountID == muleL1 {
			role = "Direct Recipient (Hop 1)"
			status = "HIGH CONTAMINATION"
		} else if c.AccountID == muleL2 {
			role = "Layered Recipient (Hop 2)"
			status = "ELEVATED CONTAMINATION"
		}

		fmt.Fprintf(w, "%s\t%s\t%.3f\t%s\n", c.AccountID, role, c.RiskScore, status)
	}
	w.Flush()
	fmt.Println("=======================================================")
}

func runCanaryStatus(args []string) {
	fs := flag.NewFlagSet("canary-status", flag.ExitOnError)
	pathA := fs.String("a", "configs/rules.yaml", "Production ruleset (A)")
	pathB := fs.String("b", "configs/rules_v4.yaml", "Candidate Shadow ruleset (B)")
	samples := fs.Int("samples", 5000, "Number of evaluation samples")
	_ = fs.Parse(args)

	cfgA, errA := rules.LoadConfig(*pathA)
	if errA != nil {
		fmt.Fprintf(os.Stderr, "Failed to load rules A: %v\n", errA)
		os.Exit(1)
	}
	cfgB, errB := rules.LoadConfig(*pathB)
	if errB != nil {
		fmt.Fprintf(os.Stderr, "Failed to load rules B: %v\n", errB)
		os.Exit(1)
	}

	prodEng := rules.NewEngine(cfgA)
	shadowEng := rules.NewEngine(cfgB)
	runner := canary.NewCanaryRunner(prodEng, shadowEng)

	records := loadEvaluationRecords("", "", *samples)
	accountHistories := make(map[uuid.UUID][]StoredTxn)
	accountProfiles := make(map[uuid.UUID]*features.WelfordProfile)
	accountCountries := make(map[uuid.UUID]map[string]time.Time)
	accountLastCountry := make(map[uuid.UUID]string)
	accountLastTxnAt := make(map[uuid.UUID]time.Time)
	accountFirstSeen := make(map[uuid.UUID]time.Time)

	for _, rec := range records {
		t := events.TransactionCreated{
			TransactionID: rec.TxnID,
			AccountID:     rec.AccountID,
			AmountMinor:   rec.AmountMinor,
			Currency:      "GBP",
			Merchant:      rec.Merchant,
			Country:       rec.Country,
			Channel:       rec.Channel,
		}

		hist := accountHistories[t.AccountID]
		prof := accountProfiles[t.AccountID]
		if prof == nil {
			prof = &features.WelfordProfile{}
			accountProfiles[t.AccountID] = prof
		}
		countries := accountCountries[t.AccountID]
		if countries == nil {
			countries = make(map[string]time.Time)
			accountCountries[t.AccountID] = countries
		}
		firstSeen, ok := accountFirstSeen[t.AccountID]
		if !ok {
			firstSeen = rec.OccurredAt
			accountFirstSeen[t.AccountID] = firstSeen
		}

		count1m, count10m := 0, 0
		var sum1h int64
		merchantSeen := false
		for _, prev := range hist {
			if prev.OccurredAt.After(rec.OccurredAt.Add(-1*time.Minute)) && !prev.OccurredAt.After(rec.OccurredAt) {
				count1m++
			}
			if prev.OccurredAt.After(rec.OccurredAt.Add(-10*time.Minute)) && !prev.OccurredAt.After(rec.OccurredAt) {
				count10m++
			}
			if prev.OccurredAt.After(rec.OccurredAt.Add(-1*time.Hour)) && !prev.OccurredAt.After(rec.OccurredAt) {
				sum1h += prev.Txn.AmountMinor
			}
			if prev.Txn.Merchant == t.Merchant {
				merchantSeen = true
			}
		}

		f := &rules.Features{
			Txn:            t,
			At:             rec.OccurredAt,
			Count1m:        count1m,
			Count10m:       count10m,
			Sum1h:          sum1h,
			ProfileN:       prof.N,
			MeanLog:        prof.MeanLog,
			StdLog:         prof.StdLog(),
			KnownCountries: countries,
			LastCountry:    accountLastCountry[t.AccountID],
			LastTxnAt:      accountLastTxnAt[t.AccountID],
			AccountAge:     rec.OccurredAt.Sub(firstSeen),
			MerchantSeen:   merchantSeen,
		}

		runner.Evaluate(f)

		prof.Update(t.AmountMinor)
		accountHistories[t.AccountID] = append(hist, StoredTxn{Txn: t, OccurredAt: rec.OccurredAt})
		if t.Country != "" {
			countries[t.Country] = rec.OccurredAt
			accountLastCountry[t.AccountID] = t.Country
		}
		accountLastTxnAt[t.AccountID] = rec.OccurredAt
	}

	rep := runner.GetReport()

	fmt.Println("\n=======================================================")
	fmt.Println("   Live Differential Shadow Canary Evaluation Report   ")
	fmt.Println("=======================================================")
	fmt.Printf("Production Version:   %s\n", rep.ProdVersion)
	fmt.Printf("Shadow Version:       %s\n", rep.ShadowVersion)
	fmt.Printf("Total Transactions:   %d\n", rep.TotalEvaluated)
	fmt.Printf("Production Flags:     %d (%.2f%%)\n", rep.ProdFlags, float64(rep.ProdFlags)/float64(rep.TotalEvaluated)*100)
	fmt.Printf("Shadow Flags:         %d (%.2f%%)\n", rep.ShadowFlags, float64(rep.ShadowFlags)/float64(rep.TotalEvaluated)*100)
	fmt.Printf("Concordance:          %.1f%%\n", rep.ConcordancePct)
	fmt.Printf("Shadow Unique Flags:  %d (New potential fraud caught)\n", rep.ShadowUniqueCount)
	fmt.Printf("Prod Unique Flags:    %d (Alerts dropped by shadow)\n", rep.ProdUniqueCount)
	fmt.Printf("Latency Delta:        %.2f μs\n\n", rep.AvgLatencyDeltaMicros)

	if rep.SafeToPromote {
		fmt.Println("Promotion Safety:     [PASS - SAFE TO PROMOTE]")
	} else {
		fmt.Println("Promotion Safety:     [BLOCKED - EXCESSIVE DIVERGENCE]")
	}
	fmt.Printf("Rationale:            %s\n", rep.SafetyRationale)
	fmt.Println("=======================================================")
}

func runSAR(args []string) {
	fs := flag.NewFlagSet("sar", flag.ExitOnError)
	acctStr := fs.String("account-id", "", "Target account UUID (optional)")
	amountMinor := fs.Int64("amount", 1250000, "Suspicious amount minor (default £12,500.00)")
	_ = fs.Parse(args)

	var acctID uuid.UUID
	if *acctStr != "" {
		acctID, _ = uuid.Parse(*acctStr)
	}
	if acctID == uuid.Nil {
		acctID = uuid.New()
	}
	flagID := uuid.New()

	history := []events.TransactionCreated{
		{TransactionID: uuid.New(), AccountID: acctID, AmountMinor: *amountMinor / 3, Country: "GB", Merchant: "CryptoOnramp"},
		{TransactionID: uuid.New(), AccountID: acctID, AmountMinor: *amountMinor / 3, Country: "JP", Merchant: "TokyoForex"},
		{TransactionID: uuid.New(), AccountID: acctID, AmountMinor: *amountMinor / 3, Country: "RU", Merchant: "BoutiqueDirect"},
	}

	signals := []rules.Signal{
		{Rule: "impossible_travel", Score: 0.96, Reason: "Superhuman velocity between London and Tokyo in 14 minutes"},
		{Rule: "amount_outlier", Score: 0.98, Reason: "Aggregate £12,500.00 exceeds customer average by 18.2-sigma"},
		{Rule: "new_country", Score: 0.88, Reason: "First recorded multi-jurisdiction activity in JP and RU"},
	}

	dossier := sar.GenerateDraft(acctID, flagID, history, signals, "STRUCTURING_LAYERED_MULE_RING")

	fmt.Println("\n=======================================================")
	fmt.Println("    AUTOMATED REGULATORY SUSPICIOUS ACTIVITY REPORT    ")
	fmt.Println("=======================================================")
	fmt.Printf("SAR Filing Ref:      %s\n", dossier.ReportID)
	fmt.Printf("Filing Date:         %s\n", dossier.FilingDate.Format(time.RFC3339))
	fmt.Printf("Reporting Entity:    %s\n", dossier.ReportingEntity)
	fmt.Printf("Primary Typology:    %s\n", dossier.PrimaryTypology)
	fmt.Printf("Suspect Account:     %s\n", dossier.SuspectAccountID)
	fmt.Printf("Total Volume GBP:    £%.2f (%d itemized transactions)\n\n", dossier.TotalSuspiciousGBP, dossier.ItemizedTxnsCount)

	fmt.Println("--- Regulatory Narrative (FinCEN Part V Compliance) ---")
	fmt.Println(dossier.Narrative)
	fmt.Println("=======================================================")
}

func runConsortiumQuery(args []string) {
	fs := flag.NewFlagSet("consortium-query", flag.ExitOnError)
	tokenStr := fs.String("token", "card_pan_compromised_darkweb_9918", "Card PAN / device ID to query")
	_ = fs.Parse(args)

	mesh := consortium.NewConsortiumNetwork("global-consortium-federation-salt-2026")

	// Pre-populate consortium network with known compromised cards reported by peer institutions
	stolenPAN1 := "card_pan_compromised_darkweb_9918"
	stolenPAN2 := "device_atm_skimmer_syndicate_cluster_01"

	bankSalt1 := "barclays-uk-salt"
	bankSalt2 := "revolut-eu-salt"

	mesh.ReportCompromised(consortium.BlindToken(stolenPAN1, mesh.FederationSalt(), bankSalt1))
	mesh.ReportCompromised(consortium.BlindToken(stolenPAN1, mesh.FederationSalt(), bankSalt2))
	mesh.ReportCompromised(consortium.BlindToken(stolenPAN2, mesh.FederationSalt(), bankSalt1))

	queryBlindToken := consortium.BlindToken(*tokenStr, mesh.FederationSalt(), bankSalt1)
	isFlagged, reportCount := mesh.CheckEntity(queryBlindToken)

	fmt.Println("\n=======================================================")
	fmt.Println("   Cryptographic Zero-PII Consortium Mesh Query        ")
	fmt.Println("=======================================================")
	fmt.Printf("Input Entity (Raw):    %s\n", *tokenStr)
	fmt.Printf("Blind Token (Double):  %s\n", queryBlindToken[:24]+"...")
	fmt.Printf("Consortium Match:      %v\n", isFlagged)
	if isFlagged {
		fmt.Printf("Reporting Banks:       %d peer financial institutions\n", reportCount)
		fmt.Println("Threat Assessment:     [CRITICAL - CONFIRMED COMPROMISE ACROSS CONSORTIUM]")
	} else {
		fmt.Println("Threat Assessment:     [CLEAN - NO CONSORTIUM ADVERSE RECORD]")
	}
	fmt.Println("=======================================================")
}

func runBanditTune(args []string) {
	fs := flag.NewFlagSet("bandit-tune", flag.ExitOnError)
	episodes := fs.Int("episodes", 500, "Number of learning simulation episodes")
	_ = fs.Parse(args)

	optimizer := bandit.NewBanditOptimizer()
	rng := rand.New(rand.NewSource(42))

	fmt.Println("\n=======================================================")
	fmt.Println("   Thompson Sampling Contextual Dynamic Thresholding   ")
	fmt.Println("=======================================================")
	fmt.Printf("Running %d online Thompson Sampling learning episodes...\n\n", *episodes)

	for i := 0; i < *episodes; i++ {
		arm := optimizer.SelectArm()
		// Ground truth simulation: Balanced threshold (0.50) has the optimal real-world trade-off
		isTrueFraud := rng.Float64() < 0.25 // 25% true fraud incidence

		if arm.ID == "balanced" {
			if isTrueFraud {
				optimizer.RecordFeedback(arm.ID, true, 25000) // £250.00 saved
			} else {
				// Very low false decline rate (2%)
				optimizer.RecordFeedback(arm.ID, rng.Float64() > 0.02, 0)
			}
		} else if arm.ID == "conservative" {
			// Blocks fraud well but creates high false positive customer friction (18%)
			if isTrueFraud {
				optimizer.RecordFeedback(arm.ID, true, 25000)
			} else {
				optimizer.RecordFeedback(arm.ID, rng.Float64() > 0.18, 0)
			}
		} else {
			// Lenient/permissive lets too much fraud through
			if isTrueFraud {
				optimizer.RecordFeedback(arm.ID, rng.Float64() > 0.40, 25000)
			} else {
				optimizer.RecordFeedback(arm.ID, true, 0)
			}
		}
	}

	summaries := optimizer.GetSummaries()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Policy Arm\tThreshold\tPulls\tPosterior α\tPosterior β\tWin Rate %\tNet Value (GBP)")
	fmt.Fprintln(w, "----------\t---------\t-----\t-----------\t-----------\t----------\t---------------")
	for _, s := range summaries {
		fmt.Fprintf(w, "%s\t%.2f\t%d\t%.1f\t%.1f\t%.1f%%\t£%.2f\n",
			s.ArmID, s.Threshold, s.Pulls, s.Alpha, s.Beta, s.ExpectedWinPct, s.NetRewardGBP)
	}
	w.Flush()
	fmt.Println("=======================================================")
}

func runBiometricsCheck(args []string) {
	fs := flag.NewFlagSet("biometrics-check", flag.ExitOnError)
	synthetic := fs.Bool("synthetic", true, "Simulate synthetic bot keystrokes (true) vs human (false)")
	_ = fs.Parse(args)

	var profile *biometrics.BiometricProfile

	if *synthetic {
		profile = &biometrics.BiometricProfile{
			InterKeyDelaysMs: []float64{8.0, 8.0, 8.0, 8.0, 8.0, 8.0}, // Sub-10ms uniform bot speed
			KeyHoldTimesMs:   []float64{12.0, 12.0, 12.0, 12.0},
			MouseTrajectory: []biometrics.Point{
				{X: 10, Y: 10, T: 10},
				{X: 50, Y: 50, T: 20},
				{X: 100, Y: 100, T: 30},
			},
			FormCompletionMs: 180.0, // Sub-second automation
		}
	} else {
		profile = &biometrics.BiometricProfile{
			InterKeyDelaysMs: []float64{95.0, 140.0, 68.0, 220.0, 110.0, 85.0},
			KeyHoldTimesMs:   []float64{65.0, 80.0, 72.0, 90.0},
			MouseTrajectory: []biometrics.Point{
				{X: 15, Y: 22, T: 100},
				{X: 42, Y: 85, T: 240},
				{X: 98, Y: 145, T: 420},
				{X: 180, Y: 175, T: 750},
			},
			FormCompletionMs: 5800.0, // 5.8s natural human entry
		}
	}

	verdict := biometrics.Analyze(profile)

	fmt.Println("\n=======================================================")
	fmt.Println("     Behavioral Biometrics Neuromuscular Dynamics      ")
	fmt.Println("=======================================================")
	fmt.Printf("Profile Type:           %s\n", map[bool]string{true: "Synthetic Automation Bot", false: "Human Shopper"}[*synthetic])
	fmt.Printf("Robotic Classification: %v\n", verdict.IsRobotic)
	fmt.Printf("Anomaly Score:          %.2f / 1.00\n", verdict.AnomalyScore)
	fmt.Printf("Cadence Std Dev:        %.2f ms (Neuromuscular jitter)\n", verdict.CadenceStdDevMs)
	fmt.Printf("Trajectory Jitter:      %.3f (Curvature entropy)\n\n", verdict.TrajectoryJitter)

	if len(verdict.RiskSignals) > 0 {
		fmt.Println("Detected Anomalies:")
		for _, s := range verdict.RiskSignals {
			fmt.Printf("  • %s\n", s)
		}
	} else {
		fmt.Println("Natural neuromuscular typing and flight paths verified. No automation detected.")
	}
	fmt.Println("=======================================================")
}

