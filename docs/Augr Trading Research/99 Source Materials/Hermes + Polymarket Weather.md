---
title: "Hermes Agent + Polymarket - how i built self-learning weather trading bot $100 → $5,000 ( guide )"
source: "https://x.com/0xMovez/status/2045080054917476451"
author:
  - "[[@0xMovez]]"
published: 2026-02-25
created: 2026-06-07
description: "My last article on how I built the Weather Clawdbot agent for Polymarket trading reached 2.5M views and helped many Polymarket traders and d..."
tags:
  - "clippings"
---
![[003 Resources/Assets/f907af58ee64bd5c1f2468cefa05d965_MD5.jpg]]

My last article on how I built the Weather Clawdbot agent for Polymarket trading reached 2.5M views and helped many Polymarket traders and developers. A month ago, [@NousResearch](https://x.com/@NousResearch) released the Hermes agent, I started using it daily for research & Polymarket trading - and I can confidently say it’s the best agent on the market.

It’s no secret that agents and bots on Polymarket earn millions of dollars daily in areas like weather trading, crypto, and sports markets. In these fields, the right algorithm and execution speed are everything - that’s why automated, self-learning AI agents have such a strong edge. They don’t sleep, they don’t have emotions, and they remember everything.

> **Example of such agents/bots:**

• ColdMath - weather trading bot that turned $300 → $219K in 3 months. \> Profile: [https://polymarket.com/@coldmath?via=following](https://polymarket.com/@coldmath?via=following)

![[003 Resources/Assets/f0a83fa0f2589597e7e9caadf0711150_MD5.jpg]]

• Sharky6999 - crypto trading bot with $819K PnL and a 99,3% win-rate >Profile: [https://polymarket.com/@sharky6999?via=following](https://polymarket.com/@sharky6999?via=following)

![[003 Resources/Assets/c23e347abcaa54821d5446885dc3bc91_MD5.jpg]]

• RN1 - best Polymarket sports bot, turned a $1.2K deposit into $7.3M > Profile: [https://polymarket.com/@rn1?via=following](https://polymarket.com/@rn1?via=following)

![[003 Resources/Assets/8d9426dae2ecf11cbf005ac91e011633_MD5.jpg]]

> In this article we will discorever: what Hermes Agent is, why it replaced OpenClaw, step-by-step install, where to find 700+ skills, and a complete walkthrough of building a Polymarket weather trading agent.

Lets start !

# What Is Hermes Agent ?

![[003 Resources/Assets/e9500c0ed6ba5fbe41cb0ea5d19c15a2_MD5.jpg]]

Hermes Agent is an open-source, self-hosted AI agent built by [N](https://x.com/NousResearch)ous Research - the team behind YaRN, Nomos, and the Psyche model families, that was released on February 25, 2026.

> Feb 25
> 
> Meet Hermes Agent, the open source agent that grows with you. Hermes Agent remembers what it learns and gets more capable over time, with a multi-level memory system and persistent dedicated machine access.

Here's the simplest way to understand it. When you hear "AI," your brain imagines a chatbot. Hermes is not a chatbot. It's more like hiring a staff member who never sleeps, remembers everything you've ever told them, gets better at their job every single day, and is always available through your phone - even when your computer is off.

## 3 things that make Hermes different:

Hermes through a three-layer model that resonated widely in the community - and it's the clearest way to understand what Hermes actually does for you.

![[003 Resources/Assets/6ba1f643c8abc362229de46dd2b31b75_MD5.jpg]]

screenshot is taken from [@blockmates](https://x.com/@blockmates) article about Hermes agent

- **Knowledge Layer:** Built-in memory, session search, LLM-Wiki skill, optional Honcho integration. Agent doesn't just answer - it accumulates knowledge over time
- **Execution Layer:** Multi-agent profiles, child agents, tool system, MCP support, persistent machine access. Agent doesn't just respond - it decomposes tasks, runs them in parallel, and delegates
- **Output Layer:** Cron jobs, gateway delivery to Telegram/Slack/Discord, Web UI, file outputResults flow back into your real workflow - not trapped in a chat window.

**Here are the top 3 things that make it the #1 agent in the market for me:**

> **1\. Persistent Memory**

![[003 Resources/Assets/0a76022046cec764919bde0b4f897985_MD5.jpg]]

Hermes doesn't forget you when you close the tab. It stores two core memory files: MEMORY.md (environment facts, conventions, experiences) and USER.md (your preferences, communication style, expectations). These are loaded as a frozen snapshot into every new session. It also stores full conversation history in a SQLite database with full-text search. Past conversations are searchable, summarizable, and retrievable on demand.

> **2\. Self-Improving Skills**

![[003 Resources/Assets/bc73ca6375c28a03af982f8308c1a7b4_MD5.jpg]]

This is the killer feature. After completing a complex task (roughly 5+ tool calls), Hermes automatically creates a "skill" -a structured markdown file that captures the procedure, known pitfalls, and verification steps. Next time it encounters a similar task, it loads that skill and executes faster, better, and the way you prefer. The longer you run Hermes, the smarter it gets at your specific workflows.

> **3\. Always-On Execution**

![[003 Resources/Assets/9718693812485e297310704f72a558cc_MD5.jpg]]

Hermes runs on your server 24/7. It connects to Telegram, Discord, Slack, WhatsApp, Signal, Email, and 15+ other platforms through a single gateway process. You message it whenever. It has built-in cron scheduling - you can write natural-language schedules like "Every morning at 8am, scan these GitHub repos and summarize changes to my Telegram." It runs unattended.

> **The practical impact is of those features:**

> Apr 13

As [@0xJeff](https://x.com/@0xJeff) described in his article after three weeks of using Hermes as a personal analyst: instead of opening Telegram, X, Rabby wallet, and Coingecko every morning. He just open Discord, check what Hermes has prepared, give feedback, and the agent improves. Total cost: $5–$10/month.

## Why Hermes is winning over OpenClaw

![[003 Resources/Assets/45bf310b84bb29796df9ea3c153bac66_MD5.jpg]]

OpenClaw (formerly Clawdbot, then Moltbot) was the breakout AI agent of early 2026. It hit 145,000+ GitHub stars, gained 20,000 stars in a single 24-hour period, and even drove Mac Mini sales to sell-out levels. It was the project that proved personal AI agents are possible and desirable.

But users who've tried both are migrating to Hermes. Here is two mains points for me:

> **1\. Learning Loop: fundamental difference**

OpenClaw is a static agent framework.

You install skills manually. They don't improve. Your agent on day 30 performs identically to your agent on day 1 -unless you manually update its configuration.

Hermes has a closed learning loop.

Every ~15 tool calls, it pauses, reviews what worked and what failed, and writes a reusable skill. These skills aren't hidden - they're readable, editable markdown files in ~/.hermes/skills/. You can review them, tweak them, delete bad ones. But the default behavior is: the agent gets measurably better with use.

> **2\. Memory: Bounded vs. Manual**

OpenClaw's memory between sessions requires manual setup and third-party tools (QMD or similar augmentation). Without these add-ons, each session starts with limited context from prior work.

Hermes has built-in bounded, curated memory. The key word is bounded - Hermes doesn't blindly stuff everything into context. It keeps high-value, low-change information persistent, and retrieves historical sessions on demand via full-text search. It also actively manages its own memory, including intelligent context compression during long sessions.

## Complete comparison openclaw/hermes

![[003 Resources/Assets/d281a8b6117cc8a10dac96ca8b5d592d_MD5.jpg]]

OpenClaw proved the concept. Hermes perfected the architecture. If you need 50+ platform integrations and don't care about self-improvement, OpenClaw still has broader coverage.

But if you want an agent that compounds in capability, has stronger security, and gets smarter the more you use it - Hermes is the clear choice in April 2026.

# How to install hermes agent ( guide )

The install takes under 5 minutes. Hermes supports Linux, macOS, and WSL2. Native Windows is not supported - use WSL2 (Ubuntu) if you're on Windows.

> **Step 1 - prepare VPS**

I want my agent to run 24/7, so I will be deploying it on a Hetzner VPS server.

![[003 Resources/Assets/4182f385e022acd1cf9c725eeaf2d616_MD5.jpg]]

Create an account at hetzner .com → pass simple KYC verification and rent a VPS server (regular performance) in the admin panel.

> **Step 2 - connect to VPS**

Run the command below in your Mac Terminal or use "Termius" for Windows.

```bash
ssh root@server_ip
```

> **Step 3 - install Hermes**

Run one command below. The installer handles platform-specific setup automatically.

![[003 Resources/Assets/1d46c0e6f006c9ceedac6fba407d4a7a_MD5.png]]

```bash
curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash
source ~/.bashrc
```

> **Step 4 - choose model**

After installation is done, the guide will prompt you to choose a model. I recommend using ChatGPT (Codex) or Anthropic (Claude).

![[003 Resources/Assets/53d4a228b4e0143b4ad485af09ebabc9_MD5.jpg]]

> **Step 5 - set up messaging gateway**

Run command below to connects Hermes to your messaging platform - so you can talk to it from your phone.

![[003 Resources/Assets/1009c191ffaaa09659031ee60ce9d4b4_MD5.jpg]]

I'm choosing Telegram myself, so I created a new bot at [@BotFather](https://x.com/@BotFather) and provided its token to my Hermes agent.

```bash
hermes gateway setup
```

> **Step 6 - start Hermes**

That's it. You now have a running agent with an interactive CLI. Start giving it tasks or message it from Telegram.

![[003 Resources/Assets/a65f480ede1f2e74c2a442665936f709_MD5.jpg]]

```bash
hermes
```

We have installed the Hermes agent on our VPS server so it can trade and learn 24/7. Now let's apply the full power of this framework to trading on Polymarket weather markets.

## Hermes - weather trading agent ( guide )

![[003 Resources/Assets/781ae69c498dbffe01784863e143d359_MD5.jpg]]

Now the payoff. We're going to deploy a production-grade, open-source Polymarket weather Hermes Agent so it runs 24/7, alerts you via Telegram, and self-calibrates over time. This is the exact setup I use to run scans across 20 cities across 4 continents with three forecast sources feeding into Expected Value and Kelly Criterion position sizing.

- we will use the logic of a weather trading bot made by [@AlterEgo\_eth](https://x.com/@AlterEgo_eth)

This step-by-step guide - just copy-paste these prompts to your Hermes agent. You don't need to code anything. Total time: ~30 minutes.

> **PROMPT 1 - Clone and setup**

We will use an open-source bot made by AlterEgo to set up basic logic for our Hermes agent, so we send this command to our agent's CLI or TG bot.

```bash
clone this repo and set up the python environment:

git clone https://github.com/alteregoeth-ai/weatherbot.git
cd weatherbot

create a python venv in the weatherbot folder and install these packages: py-clob-client python-dotenv requests web3

make sure python3.12-venv is installed if venv creation fails
```

> **PROMPT 2 - Create wallet**

I recommend setting up a separate wallet for your weather bot. It's better to ask Hermes to set up a wallet for itself, then fund it.

```bash
create a new Polygon wallet for me using eth_account in python. show me the address and private key. save the private key to weatherbot/.env file as:

PK=the_private_key
WALLET=the_address
SIG_TYPE=0"
```

Save the private key & wallet address that the bot will send you

> **PROMPT 3 - Fund the wallet (you do this yourself)**

Send to your wallet address on Polygon network: $ USDC.e - your trading capital ($10 min, $50 recommended), $ POL - about 2 POL for gas (~$1)

Once funded, tell your agent:

```bash
check the balance of my wallet on Polygon. address is 0xYOUR_ADDRESS. check both POL and USDC.e (contract: 0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174)
```

> **PROMPT 4 - Approve Polymarket contracts**

Approve all contracts so the bot can use your funds for real trading

```bash
i need to approve USDC.e spending for 3 Polymarket contracts on Polygon. my wallet private key is in the .env file in the weatherbot folder.

send on-chain ERC20 approve (max uint256) transactions for USDC.e (0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174) to these 3 spenders:

1. CTF Exchange: 0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E
2. Neg Risk Exchange: 0xC5d563A36AE78145C45a50134d48A1215220f80a
3. Router: 0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296

also approve the Conditional Tokens contract (0x4D97DCd97eC945f40cF65F87097ACe5EA0476045) using setApprovalForAll for the same 3 spenders above. this is needed for selling positions later.

use web3.py, EIP-1559 tx type, 200 gwei maxFeePerGas, wait for each receipt. chain id is 137 (Polygon). verify all approvals after.
```

> **PROMPT 5 - Configure and connect weather API**

Create an account at [visualcrossing.com](https://visualcrossing.com/) and get free API here, then send this prompt to your agent.

```bash
edit weatherbot/config.json:
- balance: match my actual USDC balance
- max_bet: 2.0 (dollars per trade, start small)
- min_ev: 0.10 (only trade with 10%+ expected edge)
- mode: live (or paper for simulation first)
- vc_key: YOUR_API_HERE

keep everything else default
```

> **PROMPT 6 - Test scan**

Run a test scan or a paper-trading mode before starting with real money.

```bash
cd weatherbot, activate the venv, and run: python3 bot_v3.py scan
show me what trades it found and placed"

You'll see:
[LIVE] BUY Chicago D+1 | 82-83F @ $0.220 | EV +3.55 | $2.00
[LIVE] BUY Seattle D+1 | 52-53F @ $0.115 | EV +7.70 | $1.50
```

> **PROMPT 7 - Start real trading**

To start real trading, just send this command to your Hermes agent and witness the magic of a self-learning weather trading agent happening right in front of you.

```bash
start the weather bot in continuous mode as a background process, self-learning based on trades. It scans every 60 minutes. Show me the Polymarket portfolio link for my wallet.
```

As a result, you will see such a dashboard with your trade executions, and you may also ask your weather trading agent to send reports to your TG bot.

![[003 Resources/Assets/e5b3228e4012c5ad698f9dfca05a6aca_MD5.png]]

The result is a fully functional, self-learning weather trading agent that improves with each trade rather than following a fixed script. Your next task is to give the bot enough trades to learn from and clear self-adjustment instructions based on that data.