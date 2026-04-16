package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

// ensureSchema creates the candles table and promotes it to a
// TimescaleDB hypertable. Both statements are idempotent so the
// feeder can run against an existing database.
//
// pgx sends one statement per Exec via the extended query protocol,
// so the CREATE TABLE and create_hypertable calls are split.
func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const createTable = `
CREATE TABLE IF NOT EXISTS candles (
  time     TIMESTAMPTZ NOT NULL,
  symbol   TEXT NOT NULL,
  interval TEXT NOT NULL,
  open     NUMERIC,
  high     NUMERIC,
  low      NUMERIC,
  close    NUMERIC,
  volume   NUMERIC,
  UNIQUE (symbol, interval, time)
)`
	if _, err := pool.Exec(ctx, createTable); err != nil {
		return err
	}
	const makeHypertable = `SELECT create_hypertable('candles', 'time', if_not_exists => TRUE)`
	if _, err := pool.Exec(ctx, makeHypertable); err != nil {
		return err
	}

	// Enable compression on chunks older than 7 days and drop chunks
	// older than 60 days. Both calls are idempotent (if_not_exists).
	// Errors are logged but non-fatal so the feeder still starts if
	// policies already exist from a different version of TimescaleDB.
	policies := []string{
		`ALTER TABLE candles SET (timescaledb.compress, timescaledb.compress_segmentby = 'symbol,interval', timescaledb.compress_orderby = 'time')`,
		`SELECT add_compression_policy('candles', INTERVAL '7 days', if_not_exists => TRUE)`,
		`SELECT add_retention_policy('candles', INTERVAL '60 days', if_not_exists => TRUE)`,
	}
	for _, q := range policies {
		if _, err := pool.Exec(ctx, q); err != nil {
			slog.Warn("candles retention/compression policy skipped", "err", err)
		}
	}
	return nil
}

// insertCandle writes one finalized candle to the hypertable.
func insertCandle(ctx context.Context, pool *pgxpool.Pool, c exchange.Candle) error {
	const q = `
INSERT INTO candles (time, symbol, interval, open, high, low, close, volume)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (symbol, interval, time) DO NOTHING`
	_, err := pool.Exec(ctx, q,
		c.OpenTime,
		c.Symbol,
		c.Interval,
		c.Open,
		c.High,
		c.Low,
		c.Close,
		c.Volume,
	)
	return err
}

// publishCandle serializes a Candle to JSON and publishes it on the
// given Redis pub/sub channel.
func publishCandle(ctx context.Context, rdb *redis.Client, channel string, c exchange.Candle) error {
	payload, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return rdb.Publish(ctx, channel, payload).Err()
}
