Public wallet: <https://polymarket.com/@l5zn1bwom8etsk>
This guide is the exact sequence i wish someone had given me before i started.
Not theory.
Not generic programming advice.
The specific decisions that separate bots that survive from bots that die quietly while you sleep.
Follow this order because EVERY step matters.
Lets goooooooooo0o0o000o
Infrastructure (everything else depends on it)
Most developers start with strategy.
That is backwards.
Your strategy runs on top of your infrastructure.
If the infrastructure is weak, the strategy NEVER gets a fair test.
You end up blaming the logic for problems that were always in the foundation.
Here's what the foundation actually requires:

> Server location is not optional
> Polymarket's matching infrastructure has a physical location.
> Your server's distance from it determines your baseline latency on every single trade.
> Ireland / Montreal are currently the closest viable options for most operators outside the US.
> The difference between a server in the right location and a random cloud VM somewhere else is not 5ms or 10ms.
> At serious competition levels it is the difference between being in the queue and being irrelevant.
> Run your own Polygon node if copy trading is part of your strategy.
> Third party RPC services waiting for confirmed block detection add approximately 2,450ms to your detection speed.
> Your own node with direct mempool reads gets you to approximately 320ms.
> That 7x gap decides whether you copy the entry or the exit.
> Dedicated metal beats shared cloud for serious work
> Shared hosting introduces unpredictable resource contention.
> Another tenant on the same machine spikes their CPU usage and your hot path latency doubles for 200ms.
> In FIFO queue systems that 200ms means you were second when you needed to be first.
> Dedicated bare metal with pinned CPU cores for your hot path eliminates that variance.
> Pin your signal detection to one core.
> Pin your order submission to another.
> Pin your websocket handling to a third.
> Pre-build everything before windows open
> HMAC signatures.
> Request headers.
> Order bodies.
> All of it prepared before the trading window starts.
> Your hot path at execution time should be doing one thing.
> Clone the pre-built request.
> Send it.
> Nothing else.
> Every millisecond of computation at execution time is a millisecond your competitor used to get ahead of you in the queue.
> Websocket quality is why your data prolly broken
> Here is something most bot builders never figure out until they have been bleeding for weeks:
> Your strategy might be correct but your data is wrong.
> Raw Polymarket websocket connections deliver a stream of problems disguised as market data.
> Stale ticks from cached snapshots that reflect prices from before the current window.
> Duplicate messages showing the same price arriving twice and triggering double signals.
> Jitter causing ticks to arrive out of sequence making your bot think price moved when it did not.
> Gaps from brief disconnects your bot never detected but which caused it to miss critical price movements.
> Your bot processes all of this as real market information.
> It makes entries based on prices that no longer exist.
> It hedges against moves that happened 400ms ago.
> It sits out real opportunities because a duplicate tick made it think the signal already fired.
> The fix is treating your websocket layer as a data cleaning system not just a connection.
> Start every connection 15 seconds before the trading window and monitor quality actively.
> Require at least 3 clean ticks per token with no jumps above 5 cents in the final warmup period.
> If your connection fails that test, skip the window entirely.
> A bad data window that you traded is worse than a missed window.
> Run 100 to 300 parallel connections per feed and kill the slowest 10% every 4 seconds.
> Your bot takes only the first deduplicated tick from whichever connection wins.
> More connections means the probability that at least one is delivering genuinely fresh data approaches certainty.
> Drop the first tick from every new connection.
> It's almost always a cached snapshot from before you connected.
> Reject any tick with a price delta above 15 cents from your last known good price.
> Log it and skip it.
> Stagger your connection startups across a full second so each one has a different shot at fresh data.
> Track jitter per connection using an EMA score and cull the most erratic ones first during each pruning cycle.
> Running this system turns your data quality from a source of silent losses into a genuine competitive advantage.
> Strategies that seemed broken often start working immediately once the data is clean.
> Historical data and why you need your own
> There's no shortcut here that works for serious bot development.
> Public data gives you closing prices and basic strike information.
> That is enough for the simplest possible strategies.
> Anything more complex requires order book depth at every tick.
> Fill rates at every price level.
> Real slippage from actual market conditions at the times your strategy would have traded.
> None of that exists in any public dataset at the quality you need.
> Record it yourself.
> Your websocket system from previous part is already running.
> Point it at every market you care about and start storing.
> Order book depth on both sides at every tick.
> Best bid and ask.
> Fill rates.
> Your own latency measurements.
> Raw storage runs approximately 1TB per day at full coverage.
> Vectorize with NumPy as you record.
> Compress into processed arrays that eliminate the raw data after extraction.
> You get down to approximately 75GB per day and backtesting runs thousands of times faster on properly vectorized data.
> 18 months of this data reveals patterns that no amount of reasoning or simulation produces.
> At 72 cents entry the historical data shows that 63% of the time price drops at least 11 cents before resolution.
> At the same level 45% of the time it drops at least 24 cents.
> That information turns arbitrary stop loss placement into statistically grounded risk management.
> Your stops sit at levels where the historical data says significant moves actually occur.
> Not at round numbers someone picked because they felt reasonable.
> Every price point on the probability curve has a drawdown profile.
> Your data builds that map.
> Your bot uses it instead of guessing.
> Backtesting that actually tells you the truth
> Your AI backtest is lying to you.
> Not maliciously, just incompetently.
> When you feed a Polymarket strategy to Claude, it does one calculation.
> Entry price versus resolution price.
> Win or loss declared.
> Win rate calculated.
> Equity curve drawn.
> Beautiful info but useless lol.
> What it cannot model:
> The 40ms between your signal firing and your order actually reaching the book where price has already moved.
> The order book depth at your entry moment, whether 1,000 shares or 10 shares were actually available at that price.
> Adverse selection.
> Structural reality that when you place a bid 10 cents below current ask you disproportionately get filled when the market is about to move against you because informed participants are taking the other side.
> Ghost fills that corrupt your state management causing downstream decisions to be made on wrong position information.
> Other bots competing for the same fills simultaneously at the same prices.
> The result is a strategy testing at 74% win rate in simulation that runs at 52% to 56% in live markets.
> Still tradeable but completely different expectation.
> The accurate process looks like this:
> First do a 20-minute manual test on your actual infrastructure using real market data.
> Does the basic logic work at all under real conditions.
> Then run multivariate parameter sweeps with cross-validation over 2 hours.
> Does the strategy hold across different parameter combinations or is it overfit to specific historical windows.
> Then code it into your production template and dry run on a real wallet with zero balance.
> Every NSF rejection, timeout, ghost fill and unexpected API response is a signal.
> Re-backtest with those failure modes included before you touch real capital.
> Only deploy when live dry run results match your backtest within 3%.
> Not approximately.
> Three percent.
> Anything wider means your model has a structural gap that will cost you money at scale.
> One rule that kills more strategies than any other.
> A 70% win rate strategy can lose money.
> If your average entry is at 65 cents you need more than 65% win rate just to break even.
> The entry price sets your minimum required accuracy.
> You cannot fix this by filtering out high-price entries.
> That removes the most probable winners from your dataset and collapses the win rate you were relying on.
> Your strategy must naturally find lower entries with higher win probability through its signal design.
> That is a fundamental design requirement not a post-processing filter.
> Strategy development
> 99 out of 100 strategy ideas die in testing.
> That is the correct outcome not a failure rate to be discouraged by.
> The ideas that survive are almost always structural rather than predictive.
> Market maker and ladder bots consistently outperform directional strategies across the platform.
> They are NOT trying to predict outcomes.
> They are exploiting structural properties of how markets behave.
> The EV framework that drives the most profitable bots is simple in concept but powerful in application.
> At every price point from 2 cents to 95 cents there is an expected value calculation.
> EV equals probability of winning multiplied by payout minus probability of losing.
> At 2 cents one win covers 49 losses in full.
> At 80 cents you collect small consistent profits with high frequency.
> Both can be positive EV if the market is mispriced.
> The difference is your loss tolerance and required sample size to realize the statistical edge.
> A bot that EV-layers across the entire probability curve simultaneously is never idle because it is always finding mispricing somewhere on the curve.
> That is why the best performing bots run tens of thousands of trades per month and compound small edges into large returns.
> The development workflow that works in practice:
> Generate a simple idea.
> One market type.
> One clear thesis.
> Test it manually in 20 minutes against real conditions.
> Run full parameter sweeps and backtests with your own data over 2 hours.
> Code it into a clean production template in 30 minutes.
> Dry run live for 1 to 7 days depending on trade frequency.
> Only deploy if everything aligns within your 3% tolerance.
> Test entries at window open and 60 seconds into the window.
> These often show dramatically different performance profiles.
> Split your results by weekday versus weekend and by UTC hour.
> US session hours bring fast reactive traders and quick-closing inefficiencies.
> Asian session hours bring thinner books, slower reactions and different overshoot patterns.
> The same strategy can print in one regime and bleed in the other.
> Finding which regime your strategy belongs to before deployment saves weeks of live losses.
> Stop losses and risk controls
> Polymarket has no native stop loss.
> You code it or you have no protection.
> This is not a feature gap to work around.
> It's a design decision that puts full responsibility for risk management on the bot builder.
> Stop losses on prediction markets feel uncomfortable because positions can recover.
> You exit at a loss and then watch the position go to where you would have profited.
> That discomfort is the cost of protection against the scenarios where positions do not recover.
> The rare catastrophic loss that a stop prevents is worth more than the frequent small losses from positions that would have been fine.
> Code stop loss execution with zero added latency on the hot path.
> A stop that fires 300ms late fires into a meaningfully worse price than necessary on a fast-moving market.
> Pre-build the exit order the same way you pre-build entries.
> Clone and send.
> For BTC markets specifically the data consistently shows that entering above 85 cents carries a specific risk profile where a single reversal in the final seconds destroys multiple windows of accumulated profit.
> Strict filters at that range improve long-term PnL even when they reduce short-term win rate.
> Beyond individual position stops your risk framework needs two additional layers.
> A daily maximum drawdown circuit breaker.
> If total losses across all bots exceed your defined threshold the entire system pauses automatically.
> You do not learn about a bad day at 11pm.
> You learn about it at 9am when the alert fires and you still have 15 hours to investigate.
> A consecutive loss pause on individual strategies.
> After 2 to 3 consecutive losses the bot skips the next several windows automatically.
> Losses cluster around regime changes.
> Pausing when losses start prevents your bot from absorbing the entire cluster.
> You are NOT predicting when things go wrong.
> You are reacting when they already started going wrong.
> That distinction matters more than it sounds.
> Deployment and monitoring
> The deployment sequence is as important as the strategy itself.
> Rush this and you will lose capital to mistakes that a proper sequence would have caught for free.
> Phase 1 is real wallet dry run.
> Connect to real infrastructure.
> Use real API endpoints.
> Attempt real orders.
> Zero balance means zero risk.
> But every NSF rejection, every timeout, every ghost fill or unexpected behavior is real data about how your bot performs under actual market conditions.
> Run this phase until live results match your backtest within your 3% tolerance.
> This takes as long as it takes.
> Do not move to Phase 2 until the numbers align.
> Phase 2 is small capital deployment.
> Start at 10% of your intended operating capital.
> Verify that results at small size match your dry run results at the same size.
> Real money changes how edge cases manifest.
> Small size lets you discover new failure modes at manageable cost.
> Phase 3 is monitored scaling.
> Increase capital in steps.
> Verify consistency at each step before adding more.
> Monitor fill rates, win rates, execution quality and drawdown depth.
> Any metric that deviates from your established baseline is a signal that something changed and needs investigation before you add more capital on top of it.
> Daily monitoring is non-negotiable.
> Check your logs every single day.
> Build alerts that fire when unusual behavior occurs:
> Drawdown above threshold.
> Fill rate dropping.
> Consecutive losses triggering circuit breaker.
> Ghost fill detection activating repeatedly.
> You should never discover a bot problem from your monthly PnL review.
> You should discover it from an alert that fires while the problem is still small and fixable.
> Last but not least
> The gap between a bot that bleeds and a bot that prints is almost never strategy.
> It is data quality, infrastructure reliability, execution precision and testing accuracy.
> Every dev who builds on Polymarket eventually learns the same lesson.
> Fix the foundation and most strategy problems solve themselves.
> Chase strategy before the foundation is solid and you will rebuild the same broken bot in ten different configurations and wonder why none of them work.
> The bots printing consistently right now are not running secret formulas.
> They are running clean data through reliable infrastructure with properly tested strategies and disciplined risk management.
> That combination is available to anyone willing to build it correctly.
> The edge exists.
> The market is competitive but not closed.
