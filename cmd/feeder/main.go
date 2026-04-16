// Package main runs the market data ingestion service.
//
// The feeder connects to Binance websocket streams for configured
// symbols (1m + 15m klines), persists finalized candles to a
// TimescaleDB hypertable, and publishes every update onto per-symbol
// Redis pub/sub channels for downstream consumers.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/adshao/go-binance/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		logger.Error("DB_URL is required")
		os.Exit(1)
	}
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		logger.Error("REDIS_URL is required")
		os.Exit(1)
	}
	testnet := os.Getenv("BINANCE_TESTNET") != "false"
	symbols := exchange.ParseSymbols(os.Getenv("SYMBOLS"), []string{"BTCUSDT"})

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("timescale connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := ensureSchema(ctx, pool); err != nil {
		logger.Error("schema migration failed", "err", err)
		os.Exit(1)
	}

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("redis url parse failed", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis ping failed", "err", err)
		os.Exit(1)
	}

	binance.UseTestnet = testnet
	ex := exchange.NewBinanceClient(
		os.Getenv("BINANCE_API_KEY"),
		os.Getenv("BINANCE_SECRET_KEY"),
		testnet,
	)

	// Backfill and run one feeder per symbol.
	var wg sync.WaitGroup
	for _, sym := range symbols {
		backfillCandles(ctx, logger, ex, pool, sym, []string{"1m", "15m"}, 100)

		f := &Feeder{
			logger:    logger,
			symbol:    sym,
			intervals: []string{"1m", "15m"},
			db:        pool,
			rdb:       rdb,
			ex:        ex,
			testnet:   testnet,
			channel:   "candles:" + sym,
		}

		logger.Info("feeder starting",
			"symbol", f.symbol,
			"intervals", f.intervals,
			"testnet", testnet,
			"channel", f.channel,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Run(ctx)
		}()
	}
	wg.Wait()
	logger.Info("feeder stopped")
}
