# ai-trader

AI-powered BTC/USDT trading bot for **Binance Testnet**. Combines RSI + MACD + Bollinger Bands with Claude API sentiment analysis.

## Architecture

```
Binance WS ──> feeder ──> TimescaleDB
                 │
                 │ Redis pub/sub (candles:BTCUSDT)
                 v
              signal ──> Claude API (sentiment)
                 │
                 │ Redis pub/sub (signals:BTCUSDT)
                 v
             executor ──> Binance REST (testnet orders)
                 │
                 ├──> TimescaleDB (trade log)
                 └──> Telegram (notifications)
```

| Service | Description |
|---------|-------------|
| **feeder** | Ingests BTC/USDT klines (1m, 15m) via WebSocket, stores in TimescaleDB, publishes to Redis |
| **signal** | Loads 100 candles, runs RSI + MACD + Bollinger + Claude sentiment, emits BUY/SELL/HOLD |
| **executor** | Receives decisions, enforces risk rules, places market + OCO orders, logs trades, sends Telegram alerts |
| **api** | REST API for dashboard (`:8080/healthz`) |

## Prerequisites

- Go 1.25+
- Docker & Docker Compose
- [Task](https://taskfile.dev/) (`brew install go-task`)
- Python 3.10+ (backtest only)

## Setup

### 1. Get API keys

| Key | Where to get it |
|-----|-----------------|
| Binance Testnet | https://testnet.binance.vision/ — click "Log In with GitHub", then "Generate HMAC_SHA256 Key" |
| Anthropic | https://console.anthropic.com/settings/keys |
| Telegram (optional) | Message [@BotFather](https://t.me/BotFather) to create a bot, then [@userinfobot](https://t.me/userinfobot) for your chat ID |

### 2. Configure

```bash
cp .env.example .env
```

Edit `.env` and fill in your keys:

```
BINANCE_API_KEY=your_testnet_key_here
BINANCE_SECRET_KEY=your_testnet_secret_here
ANTHROPIC_API_KEY=sk-ant-your-key-here
```

The rest (DB_URL, REDIS_URL) works out of the box with docker-compose defaults.

### 3. Run

**Option A: Full Docker stack** (recommended for first run)

```bash
task dev          # builds + starts all containers
task logs         # watch all logs (Ctrl+C to stop watching)
```

**Option B: Local Go + Docker infra** (better for development)

```bash
task dev:local    # starts TimescaleDB + Redis + Grafana

# In 4 separate terminals:
go run ./cmd/feeder
go run ./cmd/signal
go run ./cmd/executor
go run ./cmd/api
```

### 4. Verify it works

```bash
# Check API is up
curl http://localhost:8080/healthz

# Watch candles being stored (in a psql shell)
task psql
SELECT count(*) FROM candles;
SELECT * FROM candles ORDER BY time DESC LIMIT 5;

# Watch signals being generated
SELECT * FROM signals ORDER BY time DESC LIMIT 10;

# Watch Redis pub/sub live
task redis-cli
> SUBSCRIBE candles:BTCUSDT
```

You should see:
1. **feeder** logs: `"msg":"candle stored"` every 1m and 15m
2. **signal** logs: `"msg":"decision"` every 15m candle close
3. **executor** logs: `"msg":"signal received"` when BUY/SELL arrives

### 5. Stop

```bash
task down         # stops all containers, preserves data volumes
```

## All Tasks

```bash
task --list       # show everything available
```

| Command | What it does |
|---------|-------------|
| `task dev` | Build + start all services in Docker |
| `task dev:local` | Start infra only, print Go run commands |
| `task down` | Stop everything |
| `task test` | `go test ./... -v` |
| `task lint` | `go vet ./...` |
| `task build` | Compile Go binaries to `bin/` |
| `task backtest` | Run Python backtest simulation |
| `task optimize` | Grid search RSI + MACD parameters |
| `task logs` | Tail all container logs |
| `task logs:feeder` | Tail feeder only |
| `task logs:signal` | Tail signal engine only |
| `task logs:executor` | Tail executor only |
| `task psql` | Open psql shell to TimescaleDB |
| `task redis-cli` | Open redis-cli shell |

## Strategy

**Technical scoring** (per bar, -3 to +3):

| Indicator | +1 (buy) | -1 (sell) |
|-----------|----------|-----------|
| RSI(14) | < 30 | > 70 |
| MACD(12,26,9) | histogram > 0 | histogram < 0 |
| Bollinger(20,2) | price < lower | price > upper |

**Sentiment** from Claude (int -5 to +5, scaled to -3 to +3).

**Final score** = technical * 60% + sentiment * 40%

| Score | Action |
|-------|--------|
| >= 1.5 | BUY |
| <= -1.5 | SELL |
| between | HOLD |

## Risk Rules

| Rule | Default |
|------|---------|
| Max risk per trade | 2% of portfolio |
| Stop-loss | 1.5% below entry |
| Take-profit | 3% above entry (2:1 R:R) |
| Max open positions | 1 |
| Daily loss limit | 5% halts trading |

## Backtesting

```bash
cd backtest
pip install -r requirements.txt

python run.py          # 6-month BTC/USDT 15m backtest
                       # prints: return, Sharpe, drawdown, win rate
                       # saves:  results/equity_curve.png

python optimize.py     # grid search RSI periods x MACD params
                       # saves:  results/optimization.csv
```

## Dashboards

| URL | Credentials |
|-----|-------------|
| Grafana | http://localhost:3000 | admin / admin |
| API health | http://localhost:8080/healthz | n/a |

## Project Structure

```
ai-trader/
├── cmd/
│   ├── feeder/        # Market data ingestion (3 files)
│   ├── signal/        # Indicator + sentiment engine (3 files)
│   ├── executor/      # Order placement + risk (4 files)
│   └── api/           # REST API (1 file)
├── internal/
│   ├── exchange/      # Binance client + Candle type
│   ├── indicators/    # RSI, MACD, Bollinger + aggregator
│   ├── sentiment/     # Claude scorer + RSS news + Redis cache
│   ├── risk/          # RiskManager (position sizing, SL/TP)
│   └── strategy/      # Weighted scoring engine
├── backtest/          # Python vectorbt scripts
├── docker-compose.yml # TimescaleDB + Redis + Grafana + Go services
├── Dockerfile         # Multi-stage Go build
├── Taskfile.yml       # All dev/build/test tasks
├── CLAUDE.md          # Context for Claude Code
└── .env.example       # All environment variables
```
