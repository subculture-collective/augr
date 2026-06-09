---
title: "Hermes + Polymarket-how to build an AI for a self-learning BTC trading agent $100 → $10,000 (guide)"
source: "https://x.com/0xRicker/status/2057840731826405747"
author:
  - "[[@0xRicker]]"
published: 2026-05-22
created: 2026-06-07
description: "Trading bots generated over $60M in profit on Polymarket in 2025–2026. 77% of that came from the Crypto UP/DOWN market - driven by persisten..."
tags:
  - "clippings"
---
![[003 Resources/Assets/a228eba85e426c6d7b1556eda5007b7f_MD5.jpg]]

Trading bots generated over $60M in profit on Polymarket in 2025–2026. 77% of that came from the Crypto UP/DOWN market - driven by persistent structural inefficiencies. Here's how to build one.

![[003 Resources/Assets/3cf40e4cfbf55c5f8a89030be0f4fb27_MD5.jpg]]

## 01 - The opportunity

### Why BTC Up/Down markets

The BTC 5-minute Up/Down market on Polymarket is one of the most inefficient segments in prediction markets. The crowd prices directional moves based on emotion - news cycles, social media, gut feel.

Meanwhile, the transition matrix of BTC price states shows something different. When the market is in a committed directional state - **the persistence is measurable**. The math knows before the crowd does.

That gap between what the math says and what the market prices is the edge. And it's repeatable, scalable, and automatable.

The agent framework we're using is **Hermes** - open-source, built by NousResearch (backed by Paradigm with $70M). By April 2026, Hermes surpassed Anthropic's Claude Code in total GitHub stars - a clear signal of how fast the developer community adopted it.

![[003 Resources/Assets/51075047f82af5586cff7205553cb58f_MD5.png]]

- 288 windows/day per asset
- 1 trade every **81 seconds**
- Edge window: **5–15%** avg gap
- Win rate: **63–72%** at p ≥ 0.87

Top Successful bots running right now

![[003 Resources/Assets/1ced96438d61a7bc10f4b8c6ba6731ef_MD5.jpg]]

Bonereaper

- High-Confidence Spread CaptureEq

> Profile: [https://polymarket.com/@bonereaper?r=joinjoinjoin#tLcpwsE](https://polymarket.com/@bonereaper?r=joinjoinjoin#tLcpwsE)

![[003 Resources/Assets/128f073d0b1d9d93f9d58299020f82ef_MD5.jpg]]

0xe1D6b514 - Dual-Mode Expected Value

> Profile: [https://polymarket.com/@0xe1d6b51521bd4365769199f392f9818661bd907?r=joinjoinjoin#9TKvd55](https://polymarket.com/@0xe1d6b51521bd4365769199f392f9818661bd907?r=joinjoinjoin#9TKvd55)

![[003 Resources/Assets/8e1d38e6aabd734f51571dc1f1490a64_MD5.jpg]]

0xB27BC932- Multi-Asset Variance ReductionEq

> Profile: [https://polymarket.com/@0xb27bc932bf8110d8f78e55da7d5f0497a18b5b82-1772569391020?r=joinjoinjoin#lIVnuAb](https://polymarket.com/@0xb27bc932bf8110d8f78e55da7d5f0497a18b5b82-1772569391020?r=joinjoinjoin#lIVnuAb)

Combined: **$2,112,019**. Three bots. One market segment. Same underlying math.

## 02 - The edge

### How the math works

The model is based on **Markov Chain analysis** of BTC price states. The core insight: price movement is not random. When the market enters a persistent directional state, the probability of continuation is measurably above 50%.

![[003 Resources/Assets/a866e2785c8c5a97efc3a78d033807fd_MD5.jpg]]

**The entry formula**

> Δ⁽ʷ⁾ = p̂⁽ʷ⁾ − q⁽ʷ⁾ ≥ ε   →   ENTER p̂ = model probability  ·  q = market price  ·  ε = 5% minimum gap

> r = (1 − q) / q At q = 0.647 → r = +54.5% per trade  ·  At q = 0.441 → r = +126.7% per trade

The bot only enters when **p(j\*,j\*) ≥ 0.87** - the Markov persistence threshold. Below that, no trade. This is why the win rate is consistently above 65% despite no directional prediction.

> Kelly f\* = p − (1−p)/b Optimal position sizing per trade  ·  f\* ≈ 0.71 at p = 0.87, b = 0.647

![[003 Resources/Assets/9ec244ddf2f3eea403eafdc2192454b8_MD5.png]]

## 03 - The stack

### What you need to build this

The entire setup runs on open-source tools. No coding required. Total cost: under $10/month

![[003 Resources/Assets/d1a1221b4b6eaccd07f5ded52d050fd1_MD5.jpg]]

> $10 min to start → $50 recommended → 2 POL for gas (~$1) → ~30 min setup

## 04 - Setup

### How to set up Hermes in 3 steps

> STEP 01

Install Atomic and launch Hermes

Go to [atomicbot.ai](https://atomicbot.ai/) → download Atomic → choose Hermes agent on the main page. You can run it locally on Mac or choose "Run in Cloud" in the top-right corner - login via Google, same interface. Move app to Applications folder after download. Atomic offers 100+ integrations, persistent memory, and support for all major AI models (Claude, ChatGPT, Gemini).

> STEP 02

Connect model API - use Claude Opus 4.7

In Atomic settings → AI Models → select Anthropic → paste your API key. Choose Claude Opus 4.7 as the model engine - it has the reasoning capacity needed for real-time market analysis and self-improvement loops. Alternatively: OpenRouter (pay-as-you-go) or OpenAI Codex (free via ChatGPT Pro).

```python
Anthropic API setup
# Atomic → Settings → AI Models → Anthropic
Model:     claude-opus-4-7-20261001
API Key:   sk-ant-...
Max tokens: 4096
Temperature: 0.2  # lower = more consistent decisions
```

> STEP 03

Connect Telegram bot to your agent

Atomic → Skills → Messengers → Telegram → Connect. Create a bot via [@BotFather](https://x.com/@BotFather) in Telegram → copy token → paste into Atomic. Done in 2 clicks. From this point your Hermes agent is live and waiting for your trading logic prompt.

## 05 - Trading logic

### Setting up the BTC trading strategy

Instead of building from scratch, use an existing GitHub repo as the base logic - then feed it to Hermes and let Claude Opus adapt it to the latest Polymarket CLOB v2.

Recommended repos

```python
aulekator/polymarket-BTC-15-Minute-Trading-Bot
Production-grade, 7-phase architecture, Grafana, Redis, SL/TP
Best for: Markov-based entries, Kelly sizing

JLowo/gengar-polymarket-bot
Quarter-Kelly, Brownian motion, calibrated vol
Best for: conservative sizing with real-world guardrails

dijenne/Polymarket-bot
Two strategies: arbitrage + momentum, auto-optimization
Best for: multi-strategy approach
```

> Step 1 - Give Hermes the trading logic prompt

```python
HERMES · PROMPT 1 — BUILD LOGIC
Build a Polymarket BTC 5-minute up/down trading agent
from this repo: github.com/aulekator/polymarket-BTC-15-Minute-Trading-Bot

Update it for Polymarket CLOB v2 and make it ready for safe live trading.

Requirements:
- Keep the existing architecture if possible
- Use Python
- Migrate execution to py_clob_client_v2
- Support SAFE_ADDRESS for Polymarket Safe/proxy wallets
- Use collateral balance terminology, not legacy USDC
- Add fee-aware trade evaluation using CLOB v2 market metadata
- Implement Markov persistence filter: enter only when p(j*,j*) ≥ 0.87
- Apply Kelly criterion for position sizing: f* = p - (1-p)/b
- Keep DRY_RUN=true by default
- Do not expose private keys in chat or logs
```

> Step 2 - Set up wallet

```python
HERMES · PROMPT 2 -WALLET SETUP
Create a Polymarket trading wallet and send me the address
so I can deposit collateral.

Approve 3 Polymarket contracts:
- CTF Exchange
- Neg Risk CTF Exchange
- Neg Risk Adapter

Confirm you understand the risks before proceeding.
```

> Step 3 - Environment config

```python
.env configuration
PRIVATE_KEY=your_wallet_key
SAFE_ADDRESS=your_safe_address
CLOB_HOST=https://clob.polymarket.com
DRY_RUN=true          # start here always
MIN_EDGE=0.05         # 5% minimum arbitrage gap
MIN_PROB=0.87         # Markov persistence threshold
MIN_BET=1.00          # $1 minimum for testing
MAX_BET=50.00         # start conservative
BANKROLL=100.00       # initial capital
```

> Step 4 - Run dry test first

```python
HERMES · PROMPT 3 — TEST
Run the bot in DRY_RUN mode for 24 hours.
After each session log:
- Number of signals detected
- Entry prices and Markov state at entry
- Simulated P/L per trade
- Win rate at p(j*,j*) threshold

Send me a summary every 6 hours via Telegram.
```

## 06 - Self-learning loop

### How the agent improves itself

This is what separates Hermes from a static bot. Claude Opus 4.7 reads the execution journal after every session and rewrites the trading rules based on what worked and what didn't.

- Trade executes

Bot enters market at p(j\*,j\*) ≥ 0.87. Every entry, exit, and P/L is logged to journal.

- Nightly review

Claude Opus reads the full journal. Analyzes which persistence thresholds performed, which windows lost, which entry prices had best EV.

- Strategy update

Opus rewrites the threshold rules, adjusts Kelly sizing, and updates MIN\_PROB and MIN\_EDGE parameters automatically.

- Next session runs with updated rules

The agent is measurably smarter after 50–100 trades. Let the AI do the heavy lifting.

- Telegram report every morning

Yesterday's trades, updated rules, today's strategy. You review, approve, it runs.

```python
HERMES · NIGHTLY LOOP PROMPT
Every day at midnight, read the trade journal from today.

Analyze:
- Which Markov states had the highest win rate
- Which entry price ranges performed best (EV per trade)
- Whether current MIN_PROB should be adjusted up or down
- Whether Kelly f* is correctly sized given recent results

Then update the .env config and strategy parameters accordingly.
Send me a summary via Telegram with:
- Today's P/L, win rate, number of trades
- What changed in the strategy and why
- Tomorrow's updated thresholds
```

## Conclusion

Polymarket trading bots have already taken a large share of the profit from manual traders - and this percentage keeps increasing daily.

With agentic frameworks like Hermes and Atomic, you don't need to be a senior developer to build your own. You need Claude Opus as the brain, a GitHub repo as the starting logic, and time for 50–100 training trades.

The self-learning loop does the rest.

> Start small. DRY\_RUN=true first. $1–$2 per trade while training. The agent improves with every trade it executes - don't rush the learning phase.

## Top example of Bots from article:

> [https://polymarket.com/@bonereaper?r=joinjoinjoin#tLcpwsE](https://polymarket.com/@bonereaper?r=joinjoinjoin#tLcpwsE) [https://polymarket.com/@0xe1d6b51521bd4365769199f392f9818661bd907?r=joinjoinjoin#9TKvd55](https://polymarket.com/@0xe1d6b51521bd4365769199f392f9818661bd907?r=joinjoinjoin#9TKvd55) [https://polymarket.com/@0xb27bc932bf8110d8f78e55da7d5f0497a18b5b82-1772569391020?r=joinjoinjoin#lIVnuAb](https://polymarket.com/@0xb27bc932bf8110d8f78e55da7d5f0497a18b5b82-1772569391020?r=joinjoinjoin#lIVnuAb)

> Fastest way to find all insights and their next trade before they blow up: [https://predictparity.com?code=ricky](https://predictparity.com/?code=ricky)