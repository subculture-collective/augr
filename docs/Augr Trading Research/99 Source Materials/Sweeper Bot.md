---
title: "My sweeper bot makes ~$525 / day passively (setup guide + public results)"
source: "https://x.com/0x_Punisher/status/2044100729133019416"
author:
  - "[[@0x_Punisher]]"
published: 2026-04-14
created: 2026-06-07
description: "Most people try to make money on Polymarket by predicting outcomes.Sweeper bots don’t predict anything.They operate after the result is alre..."
tags:
  - "clippings"
---
![[003 Resources/Assets/c717349c28a5cd9b6c127b69150e4ad7_MD5.jpg]]

Most people try to make money on Polymarket by predicting outcomes.

Sweeper bots don’t predict anything.

They operate after the result is already known, and they exploit a very specific flaw in how markets behave during the post-resolution phase.

If you understand that phase properly, you realize something important:

There are moments where assets worth exactly $1 are still being sold below $1.

Not because the market is uncertain, but because participants behave inefficiently.

Your job is to build a system that sits in that gap before anyone else does.

That's what I did.

And unlike other KOLs I am sharing my wallet publicly.

**Spoiler:**

![[003 Resources/Assets/b882a02d95c87b23c650788c004094f2_MD5.jpg]]

You can find it in the end of this article, but I recommend you to read it in full first.

## What actually happens after a market ends

When a Polymarket event resolves in reality (for example BTC clearly closes above a level), there is a delay between:

> the true outcome being obvious

> the final on-chain settlement

During that gap, trading is still open.

![[003 Resources/Assets/fcf9def7d71e17a4aa5e27b76613af19_MD5.jpg]]

**This creates a strange state where:**

> correct side is effectively guaranteed to resolve at $1

> but users are still able to sell it at any price

This is not theoretical.

**It happens because:**

> some traders want instant liquidity instead of waiting

> some bots are programmed to exit positions blindly

> some users simply misclick or panic

Your bot is not predicting anything here.

It is waiting for someone to make a mistake in a system where the final value is already known.

## Why bidding at 0.999 is not too expensive

At first glance, placing a bid at 0.999 looks pointless.

You are risking almost $1 to make a fraction of a cent.

That thinking is wrong.

If the outcome is already decided, your expected value is not probabilistic anymore.

It is deterministic.

![[003 Resources/Assets/b89687e63a1b492b3fead3e8244dfbf8_MD5.jpg]]

**If you get filled:**

> you pay 0.999

> you receive 1.000 at settlement

That difference is your profit.

The reason this works is because the constraint is not price, it is access.

You are competing for the right to absorb bad liquidity.

Which means the real variable is how early your order is sitting in the book when someone sells.

## Queue mechanics and why timing beats everything

Polymarket uses FIFO matching.

If ten bots place a bid at 0.999, the first one placed gets filled first.

Everyone else only gets what’s left, which is usually nothing.

**This creates a very specific dynamic:**

You are not competing on price.

You are competing on timestamp.

**And that leads to the evolution of this strategy:**

> Early version: bots placed bids after market close

> Next version: bots placed bids seconds before close

> Current version: bots place bids when probability approaches certainty (97-99%)

![[003 Resources/Assets/cec10dfe68a2fe2c40130f84fd031c0f_MD5.jpg]]

**Why?**

Because the earlier you enter, the higher your chance of being at the front of the queue when someone exits.

But entering too early introduces risk because the outcome might still flip.

That balance is where the real strategy lives.

## Detecting true resolution before the market declares it

This is the part most people get wrong.

You cannot rely on market closed signals.

If you wait for that, you are already late.

Instead, your bot needs to infer when the outcome is effectively decided.

**For crypto Up/Down markets, that means:**

> tracking the reference exchange price (Binance, Coinbase)

> knowing the exact resolution timestamp

> calculating whether price can realistically revert before close

![[003 Resources/Assets/4481ae41a9915e9292653d48991ac043_MD5.jpg]]

**Example logic:**

If BTC needs to stay above $70,000 and there are 3 seconds left with price at $70,200, the probability is not 99%, it is effectively 100%.

That’s when your bot should already be in the queue.

![[003 Resources/Assets/709e5d32164717415c577aeddd19e78e_MD5.jpg]]

**This requires:**

> real-time price feeds

> latency awareness

> strict timing logic

Without this, you either enter too late (no fills) or too early (taking risk).

## How the bot actually places orders

The execution layer is where most bots fail.

You are racing other bots for the same queue position, so milliseconds matter.

**A working setup includes:**

> persistent connection to Polymarket API

> pre-signed transactions or optimized signing flow

> fast RPC endpoint on Polygon

![[003 Resources/Assets/583cafe4e7e9aeb5876c5b67150aa60e_MD5.jpg]]

**When your trigger condition is met, your bot should:**

> Immediately place a high bid (e.g. 0.995-0.999 range)

> Avoid retries that delay submission

> Confirm order is live in the book

![[003 Resources/Assets/9939b30144da75b08fc2bca38dd2ee33_MD5.jpg]]

You are not testing price levels.

You are trying to claim position.

Every delay increases the chance that someone else is already ahead of you.

## Managing capital and fill probability

One mistake beginners make is locking too much capital at extreme prices without getting filled.

If your order sits at $0.999 but never executes, your capital is idle.

**To optimize this, bots usually:**

> distribute bids slightly below max (e.g. 0.992-0.998 range)

> size orders based on expected fill probability

> run across many markets simultaneously

![[003 Resources/Assets/89adc20b464655d7ea68b1ea8eda01f7_MD5.jpg]]

**This creates a trade-off:**

Higher price = better guarantee if filled, but harder to be first

Lower price = worse execution price, but easier to get filled

Serious bots dynamically adjust this range depending on competition and market behavior.

## Why this still works (and why it’s harder now)

Years ago, this strategy was extremely profitable because:

> fewer bots existed

> users made more pricing mistakes

> queue competition was low

Today, the edge still exists, but it’s compressed.

![[003 Resources/Assets/48583b73599618a6d2f56a6ae8cf8423_MD5.jpg]]

**What changed:**

> more bots fighting for first position

> better infrastructure across competitors

> fewer obvious mispricings

> additional fees

That means your edge is no longer knowing the strategy.

It’s faster execution, better timing logic and smarter capital allocation.

The idea is simple, but implementation is not.

## This is not trading, but a system design

Sweeper bots show a completely different side of Polymarket.

You’re not predicting markets.

You’re not analyzing sentiment.

**You’re building a system that:**

> identifies when outcome = known

> positions itself before others

> captures mistakes automatically

If you build it correctly, profit doesn’t come from being right.

It comes from being in the right place before everyone else realizes where that place is.

And that’s the difference between someone clicking buttons and someone running a machine that gets paid every time the market slips.

As promised, sharing my best sweeper bot so far.

**Wallet:** <[https://polymarket.com/@0x13f0bcec1e2e60ec9acc3bee4d2da2fe9694a50f-1774334442364?r=punisher](https://polymarket.com/@0x13f0bcec1e2e60ec9acc3bee4d2da2fe9694a50f-1774334442364?r=punisher)\> **Current PnL:** $8,383 in 3 weeks.