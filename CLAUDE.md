# ai-trader

AI-powered BTC/USDT trading bot for **Binance Testnet**. Fuses RSI +
MACD + Bollinger Bands with Claude API sentiment analysis.

## Architecture

Four Go services communicate via Redis pub/sub and persist state in
TimescaleDB. Python scripts are used only for offline backtesting.

```
Binance WS ──> feeder ──> TimescaleDB (candles)
                 │
                 │  Redis: candles:BTCUSDT
                 v
              signal ──> Claude API (news sentiment, cached 15 min)
                 │
                 │  Redis: signals:BTCUSDT  (BUY/SELL only, not HOLD)
                 v
             executor ──> Binance REST (testnet market + OCO orders)
                 │
                 ├──> TimescaleDB (trades table)
                 └──> Telegram (optional notifications)
```

### Services (`cmd/`)

| Service    | Responsibility |
|------------|----------------|
| `feeder`   | WS klines (1m + 15m) per symbol → TimescaleDB hypertable + Redis pub/sub (`candles:<SYMBOL>`). Backfills on reconnect. |
| `signal`   | One engine per symbol. On each 15m close: load 100 candles, run `indicators.Analyze` + `sentiment.Analyze` + `strategy.Decide`, publish BUY/SELL to Redis (`signals:<SYMBOL>`), log all decisions to `signals` table |
| `executor` | One per symbol. Subscribe to decisions, `RiskManager.CanTrade()` gate, market order, OCO bracket (SL + TP), monitor OCO fill to release position slot, log to `trades` table, Telegram notification |
| `api`      | REST API + dashboard on `:8080` (`/healthz`, `/api/balance`, `/api/candles`, `/api/signals`, `/api/trades`, `/api/stats`, `/api/position`) |

### Shared packages (`internal/`)

| Package | Key exports |
|---------|-------------|
| `exchange` | `BinanceClient` — `GetBalance`, `GetTickerPrice`, `GetKlines`, `GetOpenOrders`, `PlaceMarketOrder`, `PlaceOCOOrder`. `Candle` struct shared by all services. `BaseAsset(symbol)` extracts base from pair (e.g. BTCUSDT→BTC). `ParseSymbols(csv, defaults)` parses `SYMBOLS` env. |
| `indicators` | `RSI(closes, period)`, `MACD(closes, fast, slow, signal)`, `Bollinger(closes, period, mult)` — all return `(value, error)`. `Analyze(candles) SignalSummary` scores all three. |
| `sentiment` | `Analyzer.Analyze(ctx, asset) SentimentResult` — fetches CoinTelegraph RSS headlines per asset (BTC, ETH, SOL, BNB, XRP, DOGE, ADA), scores via Claude (`claude-haiku-4-5`), caches in Redis for 15 min. Falls back to NEUTRAL on any failure. |
| `risk` | `RiskManager` — `CanTrade()`, `CalculatePositionSize(entry)`, `CalculateStopLoss(entry)`, `CalculateTakeProfit(entry)`, `RecordLoss(amount)`, `ResetDaily()`. |
| `strategy` | `Decide(signals, sentiment) Decision` — weighted scoring (see below). |

### Strategy: weighted scoring

Each bar is scored by three indicators (each ±1, range −3 to +3):

| Indicator | +1 (buy bias) | −1 (sell bias) |
|-----------|---------------|----------------|
| RSI(14) | < 30 | > 70 |
| MACD(12,26,9) | histogram > 0 | histogram < 0 |
| Bollinger(20,2) | price < lower | price > upper |

The sentiment score (int −5..+5) is scaled to −3..+3.

```
totalScore = technicalScore * 0.7 + sentimentScore * 0.3
```

| totalScore | Action |
|------------|--------|
| >= 1.0 | BUY |
| <= −1.0 | SELL |
| between | HOLD |

### Risk rules (`RiskManager` defaults)

| Rule | Value | Method |
|------|-------|--------|
| Max risk per trade | 3% of portfolio | `CalculatePositionSize` |
| Stop-loss | 2.5% below entry | `CalculateStopLoss` |
| Take-profit | 5% above entry (2:1 R:R) | `CalculateTakeProfit` |
| Max open positions | 1 | `CanTrade` |
| Daily loss limit | 8% of portfolio → halt | `CanTrade` |

### Database tables

| Table | Written by | Purpose |
|-------|-----------|---------|
| `candles` | feeder | TimescaleDB hypertable, OHLCV per symbol+interval |
| `signals` | signal | Every decision (BUY/SELL/HOLD) with score + reason |
| `trades` | executor | Order log with entry, SL, TP, status, PnL |

All tables are auto-created on service startup (`CREATE TABLE IF NOT EXISTS`).

## Local development

```sh
cp .env.example .env           # fill in Binance + Anthropic keys
task dev:local                 # starts TimescaleDB, Redis, Grafana
# then in separate terminals:
go run ./cmd/feeder
go run ./cmd/signal
go run ./cmd/executor
go run ./cmd/api
```

Or run everything in Docker: `task dev`

## Backtesting

```sh
cd backtest && pip install -r requirements.txt
python run.py         # 6-month BTC/USDT 15m simulation
python optimize.py    # grid search RSI × MACD params
```

The Python indicators use the same formulas as Go (Wilder RSI, EMA-based
MACD, population-stddev Bollinger). The Go code is the source of truth.

## Conventions for Claude Code

- **Never trade on mainnet.** `BINANCE_TESTNET` must stay `true`.
  If you see `false` anywhere in committed code, flag it immediately.
- **No secrets in code or commits.** Use `.env` (gitignored).
- **Indicators are pure functions.** Accept `[]float64`, return
  `(value, error)`. Add a `_test.go` alongside any new indicator.
- **Risk rules are mandatory.** The executor must call
  `RiskManager.CanTrade()` and `CalculatePositionSize()` before every
  order. Do not bypass, even for "just testing".
- **Sentiment fallback.** If Claude or news fetch fails, `Analyze`
  returns NEUTRAL (score 0). The system keeps trading on technicals.
- **Module path is a placeholder.** Currently
  `github.com/mamochiro/ai-trader` — rename in `go.mod` and all
  imports when the real GitHub org is chosen.
