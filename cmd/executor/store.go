package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Trade represents a row in the trades table.
type Trade struct {
	Time       time.Time
	Symbol     string
	Action     string  // BUY | SELL
	Quantity   float64
	EntryPrice float64
	StopLoss   float64
	TakeProfit float64
	Status     string  // OPEN | CLOSED | STOPPED
	PnL        float64
}

// ensureTradesSchema creates the trades table if it does not exist.
// This is NOT a hypertable — trades are a low-volume operational
// table, not a time-series with millions of rows.
func ensureTradesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const q = `
CREATE TABLE IF NOT EXISTS trades (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  time        TIMESTAMPTZ NOT NULL,
  symbol      TEXT,
  action      TEXT,
  quantity    NUMERIC,
  entry_price NUMERIC,
  stop_loss   NUMERIC,
  take_profit NUMERIC,
  status      TEXT,
  pnl         NUMERIC
)`
	_, err := pool.Exec(ctx, q)
	return err
}

// insertTrade writes one trade row.
func insertTrade(ctx context.Context, pool *pgxpool.Pool, t Trade) error {
	const q = `
INSERT INTO trades (time, symbol, action, quantity, entry_price, stop_loss, take_profit, status, pnl)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := pool.Exec(ctx, q,
		t.Time,
		t.Symbol,
		t.Action,
		t.Quantity,
		t.EntryPrice,
		t.StopLoss,
		t.TakeProfit,
		t.Status,
		t.PnL,
	)
	return err
}
