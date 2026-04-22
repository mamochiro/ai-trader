package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Candle is a JSON-friendly candle for the API.
type Candle struct {
	Time     time.Time `json:"time"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   float64   `json:"volume"`
}

// Signal is a JSON-friendly signal row for the API.
type Signal struct {
	Time      time.Time `json:"time"`
	Symbol    string    `json:"symbol"`
	Action    string    `json:"action"`
	Score     float64   `json:"score"`
	Reason    string    `json:"reason"`
	RSI       float64   `json:"rsi"`
	MACD      float64   `json:"macd"`
	Sentiment int       `json:"sentiment"`
}

// Trade is a JSON-friendly trade row for the API.
type Trade struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Symbol     string    `json:"symbol"`
	Action     string    `json:"action"`
	Quantity   float64   `json:"quantity"`
	EntryPrice float64   `json:"entry_price"`
	StopLoss   float64   `json:"stop_loss"`
	TakeProfit float64   `json:"take_profit"`
	Status     string    `json:"status"`
	PnL        float64   `json:"pnl"`
}

func queryCandles(ctx context.Context, pool *pgxpool.Pool, symbol, interval string, limit int) ([]Candle, error) {
	const q = `
SELECT time, open, high, low, close, volume
FROM candles
WHERE symbol = $1 AND interval = $2
ORDER BY time DESC
LIMIT $3`

	rows, err := pool.Query(ctx, q, symbol, interval, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Candle
	for rows.Next() {
		var c Candle
		if err := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	// Reverse to oldest-first for charting.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func querySignals(ctx context.Context, pool *pgxpool.Pool, symbol string, limit int) ([]Signal, error) {
	const q = `
SELECT time, symbol, action, score, reason, rsi, macd, sentiment
FROM signals
WHERE symbol = $1
ORDER BY time DESC
LIMIT $2`

	rows, err := pool.Query(ctx, q, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Signal
	for rows.Next() {
		var s Signal
		if err := rows.Scan(&s.Time, &s.Symbol, &s.Action, &s.Score, &s.Reason, &s.RSI, &s.MACD, &s.Sentiment); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func queryTrades(ctx context.Context, pool *pgxpool.Pool, symbol string, limit int) ([]Trade, error) {
	const q = `
SELECT id, time, symbol, action, quantity, entry_price, stop_loss, take_profit, status, pnl
FROM trades
WHERE symbol = $1
ORDER BY time DESC
LIMIT $2`

	rows, err := pool.Query(ctx, q, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trade
	for rows.Next() {
		var t Trade
		if err := rows.Scan(&t.ID, &t.Time, &t.Symbol, &t.Action, &t.Quantity,
			&t.EntryPrice, &t.StopLoss, &t.TakeProfit, &t.Status, &t.PnL); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Stats holds aggregate trading statistics.
type Stats struct {
	TotalTrades  int     `json:"total_trades"`
	OpenTrades   int     `json:"open_trades"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	WinRate      float64 `json:"win_rate"`
	TotalPnL     float64 `json:"total_pnl"`
	TodayPnL     float64 `json:"today_pnl"`
	DailyLossPct float64 `json:"daily_loss_pct"`
	TotalSignals int     `json:"total_signals"`
	BuySignals   int     `json:"buy_signals"`
	SellSignals  int     `json:"sell_signals"`
	HoldSignals  int     `json:"hold_signals"`
}

func queryStats(ctx context.Context, pool *pgxpool.Pool, symbol string, portfolioValue float64) (Stats, error) {
	var s Stats

	// Trade stats.
	err := pool.QueryRow(ctx, `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE status = 'OPEN'),
  COUNT(*) FILTER (WHERE pnl > 0),
  COUNT(*) FILTER (WHERE pnl < 0),
  COALESCE(SUM(pnl), 0),
  COALESCE(SUM(pnl) FILTER (WHERE time >= date_trunc('day', now())), 0)
FROM trades WHERE symbol = $1`, symbol).Scan(
		&s.TotalTrades, &s.OpenTrades, &s.Wins, &s.Losses, &s.TotalPnL, &s.TodayPnL)
	if err != nil {
		return s, err
	}
	closed := s.Wins + s.Losses
	if closed > 0 {
		s.WinRate = float64(s.Wins) / float64(closed) * 100
	}
	if portfolioValue > 0 && s.TodayPnL < 0 {
		s.DailyLossPct = (-s.TodayPnL / portfolioValue) * 100
	}

	// Signal stats.
	err = pool.QueryRow(ctx, `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE action = 'BUY'),
  COUNT(*) FILTER (WHERE action = 'SELL'),
  COUNT(*) FILTER (WHERE action = 'HOLD')
FROM signals WHERE symbol = $1`, symbol).Scan(
		&s.TotalSignals, &s.BuySignals, &s.SellSignals, &s.HoldSignals)
	return s, err
}

// OpenPosition is the current active trade, if any.
type OpenPosition struct {
	Active     bool    `json:"active"`
	Action     string  `json:"action,omitempty"`
	EntryPrice float64 `json:"entry_price,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
	StopLoss   float64 `json:"stop_loss,omitempty"`
	TakeProfit float64 `json:"take_profit,omitempty"`
	Time       string  `json:"time,omitempty"`
}

func queryOpenPosition(ctx context.Context, pool *pgxpool.Pool, symbol string) (OpenPosition, error) {
	var p OpenPosition
	err := pool.QueryRow(ctx, `
SELECT action, entry_price, quantity, stop_loss, take_profit, time
FROM trades WHERE symbol = $1 AND status = 'OPEN'
ORDER BY time DESC LIMIT 1`, symbol).Scan(
		&p.Action, &p.EntryPrice, &p.Quantity, &p.StopLoss, &p.TakeProfit, &p.Time)
	if err != nil {
		// No open position — not an error.
		return OpenPosition{Active: false}, nil
	}
	p.Active = true
	return p, nil
}

// PriceChange returns the current close and the close 24h ago.
func queryPriceChange(ctx context.Context, pool *pgxpool.Pool, symbol string) (now float64, prev float64, err error) {
	err = pool.QueryRow(ctx,
		`SELECT close FROM candles WHERE symbol = $1 ORDER BY time DESC LIMIT 1`,
		symbol).Scan(&now)
	if err != nil {
		return
	}
	_ = pool.QueryRow(ctx,
		`SELECT close FROM candles WHERE symbol = $1 AND time <= now() - interval '24 hours' ORDER BY time DESC LIMIT 1`,
		symbol).Scan(&prev)
	return
}

// querySignalsForChart returns BUY/SELL signals with time for chart markers.
func querySignalsForChart(ctx context.Context, pool *pgxpool.Pool, symbol string, limit int) ([]Signal, error) {
	const q = `
SELECT time, symbol, action, score, reason, rsi, macd, sentiment
FROM signals
WHERE symbol = $1 AND action != 'HOLD'
ORDER BY time DESC
LIMIT $2`
	rows, err := pool.Query(ctx, q, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Signal
	for rows.Next() {
		var s Signal
		if err := rows.Scan(&s.Time, &s.Symbol, &s.Action, &s.Score, &s.Reason, &s.RSI, &s.MACD, &s.Sentiment); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// queryLastPrice returns the latest candle close price from the DB.
func queryLastPrice(ctx context.Context, pool *pgxpool.Pool, symbol string) (float64, error) {
	var price float64
	err := pool.QueryRow(ctx,
		`SELECT close FROM candles WHERE symbol = $1 ORDER BY time DESC LIMIT 1`,
		symbol).Scan(&price)
	return price, err
}
