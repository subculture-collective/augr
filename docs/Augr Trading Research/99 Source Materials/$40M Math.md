---
title: "The Exact Math That Pulled $40,000,000 from Polymarket (Full Roadmap)"
source: "https://x.com/0x_Discover/status/2045052337996157219"
author:
  - "[[@0x_Discover]]"
published: 2026-04-17
created: 2026-06-07
description: "While you were checking if YES + NO = $1.When you see a Polymarket market where YES is $0.62 and NO is $0.33, you think  that adds up to $0...."
tags:
  - "clippings"
---
![[003 Resources/Assets/763737ac01584caec7de1f93d75d58bb_MD5.jpg]]

**While you were checking if YES + NO =** [$1.When](https://x.com/search?q=%241.When&src=cashtag_click) you see a Polymarket market where YES is $0.62 and NO is $0.33, you think that adds up to $0.95, there's arbitrage. You're right. What you never realize is that while you're manually checking whether YES plus NO equals $1, quantitative systems are solving integer programs scanning 17,218 conditions across 2^63 possible outcomes in milliseconds.

By the time you place both orders, the spread is gone. The systems found the same violation across dozens of correlated markets, calculated optimal position sizes accounting for order book depth and fees, executed parallel trades and rotated capital into the next opportunity.

The difference isn't speed. It's mathematical infrastructure.

A research paper published in 2025 "Unravelling the Probabilistic Forest: Arbitrage in Prediction Markets" documented exactly what happened. From April 2024 to April 2025, quantitative traders extracted **$39,688,585** in guaranteed arbitrage profits from Polymarket. The top single trader made $2,009,631.76 from 4,049 trades. That's $496 guaranteed profit per trade, systematically, for a year.

**The key word is guaranteed.** This wasn't prediction. This wasn't luck. This was math identifying situations where one outcome pays $1 and you can buy it for less. No matter what happens, you profit. The only question is whether you can execute before the market corrects.

![[003 Resources/Assets/f479f5805f10dc36eaf68345a2e2a3db_MD5.png]]

# Why simple math fails the marginal polytope problem

Here's what most people get wrong. They think arbitrage detection is about checking if numbers add up. It's not. It's about solving constraint satisfaction problems over exponentially large outcome spaces.

Single market, single condition: "Will Trump win Pennsylvania?" YES: $0.48, NO: $0.52. Looks perfect. No arbitrage. Now add: "Will Republicans win Pennsylvania by 5+ points?" YES: $0.32, NO: $0.68. Both sum to $1. Still looks fine.

But there's a logical dependency. **If Republicans win by 5+ points, Trump must win Pennsylvania.** These markets aren't independent. That creates arbitrage. And no simple addition check catches it.

## The scale of the problem

For any market with n conditions, there are 2^n possible price combinations. For the 2024 US election alone, there were 305 markets — creating 46,360 possible pairs to check. The NCAA 2010 tournament market had 63 games, meaning 2^63 = 9,223,372,036,854,775,808 possible outcomes.

Brute force is impossible. So quantitative systems don't enumerate. They constrain.

![[003 Resources/Assets/7ef869b03f15de30c92c3ca07749af3b_MD5.png]]

Real example from the Duke vs Cornell basketball market: Each team has 7 possible win-count outcomes. That's 14 conditions 2^14 = 16,384 combinations. But they can't both win 5+ games because they'd meet in the semifinals. Three linear constraints replace 16,384 brute force checks.

![[003 Resources/Assets/ff781b437595ee99ff2bb4a51351bdec_MD5.png]]

**What the research found:** Out of 17,218 conditions examined, 7,051 (41%) showed single-market arbitrage. The median mispricing was $0.60 meaning markets were regularly wrong by 40%. Not close to efficient. Massively exploitable.

# Bregman projection how to calculate the perfect trade

Finding arbitrage is one problem. Calculating the optimal trade is another. You can't just "fix" prices by averaging. You need to project the current market state onto the arbitrage-free manifold while preserving the information structure.

Standard distance metrics fail here. A price move from $0.50 to $0.60 has different information content than a move from $0.05 to $0.15, even though both are 10 cents. Market makers use logarithmic cost functions where prices represent probabilities. The math must respect this.

The formula for guaranteed profit

The maximum guaranteed profit from any arbitrage trade equals the Bregman divergence between the current market state and its projection onto the arbitrage-free set:

![[003 Resources/Assets/7ddd438522dfea87d29241b01f92e344_MD5.png]]

This is what the top arbitrageur was doing 4,049 times over a year. Each trade was solving this optimization problem faster and more accurately than every other participant. The $2 million wasn't luck. It was the consistent output of a mathematical system running at scale.

# Frank-Wolfe algorithm making the impossible tractable

Computing the Bregman projection directly is intractable. The arbitrage-free set M has exponentially many vertices you'd need to enumerate every valid outcome, which is impossible at scale.

The Frank-Wolfe algorithm solves this by reducing projection to a sequence of linear programs that build the solution iteratively. Instead of finding the entire set at once, it grows an active set one vertex at a time.

- **Start with a small set**of known valid outcomes
- **Each iteration:**solve a convex optimization over the current set, then find one new vertex via integer programming
- **Convergence gap**tells you when to stop typically 50–150 iterations
- **Active set grows by one**per iteration after 100 iterations, tracking 100 vertices instead of 2^63

![[003 Resources/Assets/34273500038de3a033d492c35839d04e_MD5.png]]

The research showed a crossover point: once enough outcomes settle, integer programs solve fast enough for real-time trading. After 45 games were settled in the NCAA tournament, the first successful 30-minute projection completed. For the remaining tournament, the Frank-Wolfe method outperformed the baseline by 38% on median security prices.

**Why does it get faster over time?** As outcomes settle, the feasible set shrinks. Fewer variables, tighter constraints, faster solves. Early in a tournament: 10–30 second solve times. Late in a tournament: under 5 seconds. The system accelerates as the event unfolds.

# Execution where most strategies fail

You've detected arbitrage. You've computed the optimal trade. Now you need to execute. This is where most people get destroyed.

Polymarket uses a Central Limit Order Book. Unlike decentralized exchanges where arbitrage can be atomic, CLOB execution is sequential. Your arbitrage plan: buy YES at $0.30, buy NO at $0.30, guaranteed $0.40 profit. Reality: YES fills at $0.30 ✓. Price updates. NO fills at $0.78 ✗. You just lost $0.08.

## The latency hierarchy

![[003 Resources/Assets/544e5b53ead7b984d3a5e5c9cf38419f_MD5.png]]

The 2-second window is not a bug. It's a structural feature. Polygon block time is 2 seconds. Everyone waits for the same blockchain. The edge comes from the 30 seconds before that the detection-to-submission window where fast systems submit all legs in one block and slow systems are still reading the market.

## Why copy-trading fast wallets fails

What you think happens

- You see their profitable trade on-chain
- You copy the same position
- You capture the same profit
- You win alongside them

What actually happens

- Block N-1: system detects, submits in 30ms
- Block N: all legs confirm, arbitrage captured
- Block N+1: you see it and copy
- You paid $0.344 for what they bought at $0.322
- You're providing exit liquidity, not arbitraging

So the only way copy-trading works is if your execution fires in the same block as the original wallet. That's a hard technical requirement not a preference.

I tested a lot of bots. Most of them are too slow. By the time the Telegram signal fires, the position is already gone and you're buying into a moved market. I tried service after service looking for something that actually executes at block level.

# What was actually deployed the complete system

Theory is clean. Production is messy. Here's what a working arbitrage system actually looks like based on the research findings.

- **Data pipeline** Real-time

WebSocket connection to Polymarket CLOB API for live order book updates and trade feeds. Alchemy Polygon node for querying the OrderFilled contract events. The research analyzed 86 million transactions this requires infrastructure, not scripts.

- **Dependency detection** AI-powered

DeepSeek-R1-Distill-Qwen-32B with prompt engineering to classify market pairs. Input: two market descriptions. Output: JSON of valid outcome combinations. 81.45% accuracy on complex multi-condition election markets. Good enough for filtering; requires manual verification for execution.

- **Optimization engine** 3 layers

Layer 1: Fast linear programming relaxations for obvious mispricings (milliseconds). Layer 2: Frank-Wolfe algorithm with Gurobi IP solver for complex cases (1–30 seconds depending on market state). Layer 3: Execution validation against live order book before any order is submitted.

- **Position sizing** Kelly criterion

Modified Kelly criterion accounting for execution risk and order book depth. Position cap at 50% of available order book depth to avoid moving the market against yourself. Every position re-sized in real-time based on current portfolio value.

# The actual numbers broken down

The research broke down where the $39.7 million came from. Three distinct strategies, each exploiting a different type of market inefficiency.

![[003 Resources/Assets/279237707b21152d959a4949cef7378c_MD5.png]]

The top 10 extractors took $8,127,849 20.5% of the total. The top single extractor made $2,009,632 from 4,049 trades. Average profit per trade: $496. Not lottery wins. Not lucky timing. Mathematical precision executed systematically over 365 days.

![[003 Resources/Assets/7c9fb6f9f7035823a161d95e6d854465_MD5.png]]

## What separates winners the honest breakdown

The research makes the gap explicit. Two groups of participants, same markets, same information, completely different outcomes.

Retail approach

- Check prices every 30 seconds manually
- Look for obvious YES + NO ≠ $1
- Submit orders sequentially via UI
- No position sizing framework
- No execution risk management
- Providing exit liquidity to fast wallets

Quantitative approach

- Real-time WebSocket feeds <5ms
- Integer programming for dependency detection
- Bregman projection for optimal trades
- Frank-Wolfe with Gurobi IP solver
- Parallel order execution, same block
- Kelly-based position sizing, live recalculation

One group extracted $40 million. The other group provided the liquidity that made it possible.

**The research paper is public.** The algorithms are known. arXiv:2508.03474. The theory foundation: arXiv:1606.02825v2. The math works. The infrastructure exists. The only question is whether you can build it before the next $40 million is extracted.

## 15 bots that made more money than most people earn in a lifetime

Theory is one thing. Here's what the math looks like when it actually runs. These are real, verified Polymarket profiles all publicly visible on-chain. Their PnL curves are not luck. They are the output of systematic strategies running the same loops, day after day, with mechanical consistency.

The total across these 15 wallets: **over $51 million in documented profit.**

- [kch123](https://polymarket.com/@kch123) —Latency arb · high frequency **$12,000,000**
- [RN1](https://polymarket.com/@rn1)—Market making · multi-market **$7,400,000**
- [Swisstony](https://polymarket.com/@swisstony)—Oracle arbitrage · Chainlink **$5,900,000**
- [GamblingIsAllYouNeed](https://polymarket.com/@gamblingisallyouneed)—News-driven · AI probability **$4,600,000**
- [DrPufferfish](https://polymarket.com/@drpufferfish)—Combinatorial arb · multi-market **$3,400,000**
- [sovereign2013](https://polymarket.com/@sovereign2013)—Latency arb · BTC/ETH contracts **$3,400,000**
- [0x2a2C53bD27](https://polymarket.com/@0x2a2c53bd278c04da9962fcf96490e17f3dfb9bc1-1772479215461)—Market rebalancing · systematic **$2,500,000**
- [Countryside](https://polymarket.com/@countryside)—Election markets · base rate **$2,400,000**
- [gatorr](https://polymarket.com/@gatorr)—Latency arb · parallel execution **$2,300,000**
- [weflyhigh](https://polymarket.com/@weflyhigh)—Multi-strategy · diversified **$1,800,000**
- [blindStaking](https://polymarket.com/@blindstaking)—Market making · liquidity provision **$1,500,000**
- [CharlieKirkEvans](https://polymarket.com/@0x2a2c53bd278c04da9962fcf96490e17f3dfb9bc1-1772479215461)—Political markets · news arb **$1,200,000**
- [JPMorgan101](https://polymarket.com/profile/0xb6d6e99d3bfe055874a04279f659f009fd57be17)—Institutional-style systematic **$1,100,000**
- [cigarettes](https://polymarket.com/profile/0xd218e474776403a330142299f7796e8ba32eb5c9)—High-frequency · short contracts **$850,000**
- [Sharky6999](https://polymarket.com/profile/0x751a2b86cab503496efd325c8344e10159349ea1)—Latency arb · crypto contracts **$813,000**

# How to start trading on Polymarket

Now that you understand how the math works here's how to actually get on the platform and what's in it for you right now.

## What is the Polymarket drop?

Polymarket has been running an active rewards program for new and existing traders. New users who register and place their first trades during active campaign periods receive platform rewards USDC distributed directly to your wallet based on trading volume and activity. The program is designed to bootstrap liquidity, which means **early participants get disproportionately large rewards** relative to the volume they bring.

This is not speculation. Polymarket's rewards structure is documented on the platform and has paid out to thousands of traders. The earlier you participate, the larger your share of the reward pool.

- Register

Go to the link below and connect your wallet MetaMask or Coinbase Wallet

[Polymarket AirDrop](https://polymarket.com/)

- Fund with USDC

Deposit via Polygon network only not ETH mainnet. Bridge USDC if needed.

- Place your first trade

Pick any active market and place a small position. $10–$50 is enough to start and qualify.

- Check your rewards

Open the Rewards tab. Your current drop allocation is shown in USDC and updates with volume.

- Scale volume

Higher trading volume = larger share of the reward pool. The earlier you start the more you get.

**Why register now:** Reward pools are time-limited and distributed proportionally to early participants. The math that extracted $40M from Polymarket required millions in capital and sophisticated infrastructure. Getting rewarded for simply trading on the platform during the drop period requires neither. This is the low-barrier version of being on the right side of the market.

![[003 Resources/Assets/fac425896a7e7c51e491d5dfb4ea0406_MD5.png]]

The math works. The infrastructure exists.The only question is execution.