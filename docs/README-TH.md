# ai-trader - ระบบเทรด Crypto อัตโนมัติด้วย AI

## ภาพรวม

ระบบเทรด BTC/USDT อัตโนมัติบน **Binance Testnet** (เงินจำลอง ไม่เสียเงินจริง)
ใช้ตัวชี้วัดทางเทคนิค RSI + MACD + Bollinger Bands ร่วมกับ AI (Claude) วิเคราะห์ข่าว
เพื่อตัดสินใจซื้อ-ขาย

---

## ระบบทำงานอย่างไร

```
ข้อมูลราคา BTC จาก Binance
        │
        ▼
    ┌─────────┐
    │ feeder  │  ← ดึงราคาแบบ real-time ทุก 1 นาที และ 15 นาที
    └────┬────┘    เก็บลง Database + ส่งต่อผ่าน Redis
         │
         ▼
    ┌─────────┐
    │ signal  │  ← รอราคาปิดทุก 15 นาที แล้ว:
    │         │    1. คำนวณ RSI, MACD, Bollinger
    │         │    2. ให้ AI อ่านข่าว + ให้คะแนน sentiment
    │         │    3. รวมคะแนน → ตัดสินใจ BUY / SELL / HOLD
    └────┬────┘
         │ (ส่งเฉพาะ BUY หรือ SELL)
         ▼
    ┌──────────┐
    │ executor │  ← เมื่อได้สัญญาณ BUY หรือ SELL:
    │          │    1. ตรวจสอบ risk (เสี่ยงได้ไหม)
    │          │    2. คำนวณขนาด position
    │          │    3. ส่งคำสั่งซื้อ/ขายไป Binance
    │          │    4. ตั้ง Stop-Loss + Take-Profit อัตโนมัติ
    │          │    5. แจ้งเตือนผ่าน Telegram
    └──────────┘
```

---

## Service 4 ตัว (Go)

| Service | หน้าที่ | ทำงานตลอดเวลา? |
|---------|---------|----------------|
| **feeder** | ดึงราคา BTC แบบ real-time จาก Binance เก็บลง Database | ใช่ |
| **signal** | วิเคราะห์ตัวชี้วัด + AI sentiment ทุก 15 นาที | ใช่ |
| **executor** | ส่งคำสั่งซื้อขายเมื่อได้สัญญาณ | ใช่ |
| **api** | REST API สำหรับ health check | ใช่ |

**ทั้ง 4 ตัวต้องรันพร้อมกัน** ในแต่ละ terminal

---

## Python Scripts (ไม่ใช่ service — รันเมื่อต้องการ)

| Script | หน้าที่ |
|--------|---------|
| `backtest/run.py` | จำลองการเทรดย้อนหลัง 6 เดือน ดูว่ากลยุทธ์ได้กำไรไหม |
| `backtest/optimize.py` | ทดลองปรับค่า RSI / MACD หาค่าที่ดีที่สุด |

**Python = ห้องทดลอง** ทดสอบก่อนเสี่ยงเงินจริง
**Go = ระบบจริง** เทรดอัตโนมัติ

---

## กลยุทธ์การเทรด

### ตัวชี้วัดทางเทคนิค (3 ตัว)

| ตัวชี้วัด | สัญญาณซื้อ (+1) | สัญญาณขาย (-1) |
|-----------|-----------------|-----------------|
| **RSI(14)** | RSI < 30 (oversold/ราคาถูกเกินไป) | RSI > 70 (overbought/ราคาแพงเกินไป) |
| **MACD(12,26,9)** | histogram > 0 (แนวโน้มขาขึ้น) | histogram < 0 (แนวโน้มขาลง) |
| **Bollinger(20,2)** | ราคาต่ำกว่าเส้นล่าง | ราคาสูงกว่าเส้นบน |

แต่ละตัวให้คะแนน +1 (ซื้อ), -1 (ขาย), หรือ 0 (กลาง)
**คะแนนเทคนิค = -3 ถึง +3**

### AI Sentiment (Claude อ่านข่าว)

- ดึงข่าว Bitcoin 5 อันล่าสุดจาก CoinTelegraph (ฟรี)
- ส่งให้ Claude AI วิเคราะห์: ข่าวดี (+5) ถึง ข่าวร้าย (-5)
- cache ไว้ 15 นาที (ประหยัดค่า API)
- ถ้า Claude ล่มหรือเรียกไม่ได้ → ให้คะแนน 0 (กลาง) แล้วเทรดด้วยเทคนิคอย่างเดียว

### การรวมคะแนน

```
คะแนนรวม = (คะแนนเทคนิค × 60%) + (คะแนน sentiment × 40%)
```

| คะแนนรวม | การกระทำ |
|----------|----------|
| >= 1.5 | **BUY** (ซื้อ) |
| <= -1.5 | **SELL** (ขาย) |
| ระหว่างนั้น | **HOLD** (รอ ไม่ทำอะไร) |

---

## กฎจัดการความเสี่ยง (Risk Management)

ระบบมีกฎเหล็กป้องกันการขาดทุนหนัก:

| กฎ | ค่า | หมายความว่า |
|----|-----|-------------|
| ความเสี่ยงต่อเทรด | 2% ของพอร์ต | พอร์ต $60 → เสี่ยงได้สูงสุด $1.20 ต่อเทรด |
| Stop-Loss | 1.5% ต่ำกว่าราคาเข้า | ตัดขาดทุนอัตโนมัติ |
| Take-Profit | 3% สูงกว่าราคาเข้า | ขายทำกำไรอัตโนมัติ (reward:risk = 2:1) |
| Position สูงสุด | 1 | เปิดได้ทีละ 1 position เท่านั้น |
| ขาดทุนต่อวัน | 5% ของพอร์ต | ถ้าวันนี้ขาดทุน 5% → หยุดเทรดทั้งวัน |

---

## Database (เก็บข้อมูลอะไรบ้าง)

| ตาราง | ใครเขียน | เก็บอะไร |
|-------|---------|----------|
| `candles` | feeder | ราคา OHLCV ทุก 1 นาที และ 15 นาที |
| `signals` | signal | ทุกการตัดสินใจ (BUY/SELL/HOLD) พร้อมคะแนนและเหตุผล |
| `trades` | executor | ทุกคำสั่งซื้อขายที่ส่งไป Binance |

### ดูข้อมูลในฐานข้อมูล

```bash
task psql
```

```sql
-- ดูจำนวน candle ที่เก็บได้
SELECT interval, count(*) FROM candles GROUP BY interval;

-- ดูสัญญาณล่าสุด
SELECT time, action, score, reason FROM signals ORDER BY time DESC LIMIT 10;

-- ดูการเทรดที่เกิดขึ้น
SELECT time, action, entry_price, quantity, status FROM trades ORDER BY time DESC;

-- สรุปรวม
SELECT
  (SELECT count(*) FROM candles) AS candles,
  (SELECT count(*) FROM signals) AS signals,
  (SELECT count(*) FROM trades) AS trades;
```

---

## โครงสร้าง Infrastructure

| Service | Port | หน้าที่ |
|---------|------|---------|
| TimescaleDB | localhost:5434 | ฐานข้อมูลหลัก (เก็บ candles, signals, trades) |
| Redis | localhost:6381 | ส่งข้อมูลระหว่าง service แบบ real-time |
| Grafana | localhost:3010 | Dashboard แสดงผลกราฟ (admin / admin) |
| API | localhost:8080 | Health check endpoint |

---

## วิธีใช้งาน

### ครั้งแรก (Setup)

```bash
# 1. ตั้งค่า .env
cp .env.example .env
# แก้ไข .env → ใส่ BINANCE_API_KEY, BINANCE_SECRET_KEY, ANTHROPIC_API_KEY

# 2. เปิด Infrastructure
task dev:infra

# 3. รัน service (เปิด 4 terminal)
go run ./cmd/feeder
go run ./cmd/signal
go run ./cmd/executor
go run ./cmd/api
```

### ตรวจสอบว่าทำงานปกติ

```bash
# เช็ค API
curl http://localhost:8080/healthz   # ต้องได้ "ok"

# เช็คว่า candle เข้า DB
task psql
SELECT count(*) FROM candles;       # ต้องเพิ่มขึ้นเรื่อยๆ
```

### หยุดระบบ

```bash
# หยุด Go service → Ctrl+C ในแต่ละ terminal
# หยุด Infrastructure
task down
```

### รัน Backtest

```bash
cd backtest
pip3 install -r requirements.txt
python3 run.py         # จำลองย้อนหลัง 6 เดือน
python3 optimize.py    # หาค่าที่ดีที่สุด
```

---

## ค่าใช้จ่าย

| รายการ | ค่าใช้จ่าย |
|--------|-----------|
| Binance Testnet | **ฟรี** (เงินจำลอง) |
| TimescaleDB / Redis / Grafana | **ฟรี** (Docker บนเครื่องตัวเอง) |
| ข่าว CoinTelegraph RSS | **ฟรี** (ไม่ต้อง API key) |
| Claude API (Haiku 4.5) | **~$0.09/วัน** ($5 ใช้ได้ ~2 เดือน) |
| Telegram | **ฟรี** |

---

## ข้อควรรู้สำคัญ

1. **Testnet เท่านั้น** — ระบบนี้ตั้งค่าให้ใช้ Binance Testnet (เงินจำลอง) ห้ามเปลี่ยน `BINANCE_TESTNET=false`
2. **ต้องรอสะสมข้อมูล** — signal engine ต้องการ candle 15 นาที อย่างน้อย 35 อัน (~9 ชั่วโมง) ก่อนจะเริ่มวิเคราะห์ได้
3. **ส่วนใหญ่จะ HOLD** — ระบบจะซื้อ/ขายเมื่อตัวชี้วัด 2+ ตัวเห็นด้วย + sentiment สอดคล้อง ไม่ใช่ทุก 15 นาที
4. **ถ้า Claude ล่ม** — ระบบยังทำงานได้ โดยใช้เทคนิคอย่างเดียว (sentiment = 0)
5. **เปลี่ยน LLM ได้** — ถ้าอยากเปลี่ยนจาก Claude เป็น Gemini หรือ Groq แก้แค่ไฟล์เดียว (`internal/sentiment/claude.go`)

---

## ไฟล์สำคัญ

```
ai-trader/
├── cmd/
│   ├── feeder/        ← ดึงราคา (3 ไฟล์)
│   ├── signal/        ← วิเคราะห์สัญญาณ (3 ไฟล์)
│   ├── executor/      ← ส่งคำสั่งเทรด (4 ไฟล์)
│   └── api/           ← REST API (1 ไฟล์)
├── internal/
│   ├── exchange/      ← เชื่อมต่อ Binance
│   ├── indicators/    ← RSI, MACD, Bollinger
│   ├── sentiment/     ← Claude AI วิเคราะห์ข่าว
│   ├── risk/          ← จัดการความเสี่ยง
│   └── strategy/      ← คำนวณคะแนนรวม ตัดสินใจ BUY/SELL/HOLD
├── backtest/          ← Python ทดสอบย้อนหลัง
├── docker-compose.yml ← TimescaleDB + Redis + Grafana
├── Dockerfile         ← สร้าง container Go services
├── Taskfile.yml       ← คำสั่งลัด (task dev, task test, etc.)
├── .env.example       ← ตัวอย่าง environment variables
├── CLAUDE.md          ← context สำหรับ Claude Code
└── README.md          ← คู่มือภาษาอังกฤษ
```

---

## คำสั่งที่ใช้บ่อย

| คำสั่ง | ทำอะไร |
|--------|--------|
| `task dev:infra` | เปิด Database + Redis + Grafana |
| `task down` | ปิดทุกอย่าง |
| `task test` | รัน Go tests |
| `task psql` | เปิด SQL shell ดูข้อมูลใน Database |
| `task redis-cli` | เปิด Redis shell ดู real-time data |
| `task logs` | ดู log ของทุก container |
| `go run ./cmd/feeder` | รัน feeder service |
| `go run ./cmd/signal` | รัน signal engine |
| `go run ./cmd/executor` | รัน executor |
| `go run ./cmd/api` | รัน API server |
