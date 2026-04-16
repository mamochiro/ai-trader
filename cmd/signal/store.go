package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

// Signal represents a row in the signals table.
type Signal struct {
	Time      time.Time
	Symbol    string
	Action    string
	Score     float64
	Reason    string
	RSI       float64
	MACD      float64
	Sentiment int
}

func ensureSignalsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const createTable = `
CREATE TABLE IF NOT EXISTS signals (
  time       TIMESTAMPTZ NOT NULL,
  symbol     TEXT,
  action     TEXT,
  score      NUMERIC,
  reason     TEXT,
  rsi        NUMERIC,
  macd       NUMERIC,
  sentiment  INTEGER
)`
	if _, err := pool.Exec(ctx, createTable); err != nil {
		return err
	}
	const makeHypertable = `SELECT create_hypertable('signals', 'time', if_not_exists => TRUE, migrate_data => TRUE)`
	if _, err := pool.Exec(ctx, makeHypertable); err != nil {
		return err
	}

	// Compress chunks older than 7 days, drop chunks older than 180 days.
	policies := []string{
		`ALTER TABLE signals SET (timescaledb.compress, timescaledb.compress_segmentby = 'symbol', timescaledb.compress_orderby = 'time')`,
		`SELECT add_compression_policy('signals', INTERVAL '7 days', if_not_exists => TRUE)`,
		`SELECT add_retention_policy('signals', INTERVAL '180 days', if_not_exists => TRUE)`,
	}
	for _, q := range policies {
		if _, err := pool.Exec(ctx, q); err != nil {
			slog.Warn("signals retention/compression policy skipped", "err", err)
		}
	}
	return nil
}

func insertSignal(ctx context.Context, pool *pgxpool.Pool, s Signal) error {
	const q = `
INSERT INTO signals (time, symbol, action, score, reason, rsi, macd, sentiment)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := pool.Exec(ctx, q,
		s.Time, s.Symbol, s.Action, s.Score, s.Reason,
		s.RSI, s.MACD, s.Sentiment,
	)
	return err
}

// loadCandles fetches the most recent candles from TimescaleDB,
// returned oldest-first so indicators see the correct order.
func loadCandles(ctx context.Context, pool *pgxpool.Pool, symbol, interval string, limit int) ([]exchange.Candle, error) {
	const q = `
SELECT time, symbol, interval, open, high, low, close, volume
FROM candles
WHERE symbol = $1 AND interval = $2
ORDER BY time DESC
LIMIT $3`

	rows, err := pool.Query(ctx, q, symbol, interval, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []exchange.Candle
	for rows.Next() {
		var c exchange.Candle
		if err := rows.Scan(
			&c.OpenTime, &c.Symbol, &c.Interval,
			&c.Open, &c.High, &c.Low, &c.Close, &c.Volume,
		); err != nil {
			return nil, err
		}
		c.IsFinal = true
		candles = append(candles, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse to oldest-first for indicator computation.
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}
