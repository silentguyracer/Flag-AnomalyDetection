package main

import (
	"context"
	"flag"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"fraud-service/internal/bus"
	"fraud-service/internal/events"
)

type GeneratorConfig struct {
	Brokers     []string
	Topic       string
	RatePerSec  int
	DurationSec int
	AttackRatio float64
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	brokersStr := flag.String("brokers", "localhost:9092", "Kafka brokers comma-separated")
	topic := flag.String("topic", "transactions.created", "Kafka topic to produce to")
	rate := flag.Int("rate", 10, "Target events per second")
	duration := flag.Int("duration", 30, "Duration in seconds (0 = infinite)")
	flag.Parse()

	brokers := strings.Split(*brokersStr, ",")
	logger.Info("starting load generator",
		"brokers", brokers,
		"topic", *topic,
		"rate", *rate,
		"duration", *duration,
	)

	producer, err := bus.NewProducer(brokers)
	if err != nil {
		logger.Error("failed to create kafka producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*duration)*time.Second)
		defer cancel()
	}

	runSimulation(ctx, producer, *topic, *rate, logger)
}

func runSimulation(ctx context.Context, producer *bus.Producer, topic string, rate int, logger *slog.Logger) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	numAccounts := 50
	accountIDs := make([]uuid.UUID, numAccounts)
	accountMeans := make([]float64, numAccounts)
	for i := 0; i < numAccounts; i++ {
		accountIDs[i] = uuid.New()
		accountMeans[i] = math.Log(float64(rng.Intn(2500) + 1200)) // £12 to £37 avg
	}

	merchants := []string{"Tesco Metro", "Sainsburys", "Amazon UK", "Uber", "Deliveroo", "Costa Coffee", "Shell", "Marks & Spencer"}
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()

	sent := 0
	for {
		select {
		case <-ctx.Done():
			logger.Info("load generation completed", "total_sent", sent)
			return
		case <-ticker.C:
			acctIdx := rng.Intn(numAccounts)
			acctID := accountIDs[acctIdx]
			meanLog := accountMeans[acctIdx]

			isAttack := rng.Float64() < 0.05
			var txn events.TransactionCreated

			if !isAttack {
				amt := int64(math.Max(200, math.Exp(meanLog+rng.NormFloat64()*0.4)))
				txn = events.TransactionCreated{
					TransactionID: uuid.New(),
					AccountID:     acctID,
					AmountMinor:   amt,
					Currency:      "GBP",
					Merchant:      merchants[rng.Intn(len(merchants))],
					MCC:           "5411",
					Country:       "GB",
					Channel:       "contactless",
				}
			} else {
				// Inject an attack scenario
				scenarioType := rng.Intn(4)
				switch scenarioType {
				case 0: // Card testing cashout
					txn = events.TransactionCreated{
						TransactionID: uuid.New(),
						AccountID:     acctID,
						AmountMinor:   9500, // £95.00
						Currency:      "GBP",
						Merchant:      "CameraStoreUK",
						MCC:           "5732",
						Country:       "GB",
						Channel:       "online",
					}
				case 1: // Account takeover in new country
					txn = events.TransactionCreated{
						TransactionID: uuid.New(),
						AccountID:     acctID,
						AmountMinor:   48000, // £480.00
						Currency:      "GBP",
						Merchant:      "TokyoLuxury",
						MCC:           "5311",
						Country:       "JP",
						Channel:       "online",
					}
				case 2: // Impossible travel
					txn = events.TransactionCreated{
						TransactionID: uuid.New(),
						AccountID:     acctID,
						AmountMinor:   15000,
						Currency:      "USD",
						Merchant:      "Miami Gadgets",
						MCC:           "5732",
						Country:       "US",
						Channel:       "chip",
					}
				case 3: // Large amount outlier
					txn = events.TransactionCreated{
						TransactionID: uuid.New(),
						AccountID:     acctID,
						AmountMinor:   75000, // £750.00
						Currency:      "GBP",
						Merchant:      "HighEndJewellery",
						MCC:           "5944",
						Country:       "GB",
						Channel:       "chip",
					}
				}
			}

			env, err := events.NewEnvelope(events.EventTypeTransactionCreated, events.CurrentVersion, txn)
			if err != nil {
				logger.Error("failed to create envelope", "error", err)
				continue
			}

			if err := producer.PublishEnvelope(ctx, topic, acctID.String(), env, env.EventID.String()); err != nil {
				logger.Error("failed to produce transaction", "error", err)
			} else {
				sent++
				if sent%50 == 0 {
					logger.Info("produced batch", "sent", sent)
				}
			}
		}
	}
}
