---
title: "How to Build a Self-Calibrating Polymarket Weather Bot in Python (Complete Guide)"
source: "https://x.com/AlterEgo_eth/status/2034970007369916590"
author:
  - "[[@AlterEgo_eth]]"
published: 2026-03-20
created: 2026-06-07
description: "In Part 1, I built a basic bot: 6 US cities, NWS forecasts, paper trades on underpriced buckets. It worked, but it was just the foundation.T..."
tags:
  - "clippings"
---
![[003 Resources/Assets/eb401143f9564958b9986b4357bc27b1_MD5.jpg]]

In Part 1, I built a basic bot: 6 US cities, NWS forecasts, paper trades on underpriced buckets. It worked, but it was just the foundation.

This version goes further. 20 cities across 4 continents. Three forecast sources instead of one. Expected Value and Kelly Criterion for mathematically sound position sizing. A full data storage system - every forecast snapshot, every trade, every market resolution - so the bot can learn and self-calibrate over time. No prior experience needed. By the end you'll have a working bot running 24/7.

Bookmark this article - it's a long one.

## What you'll need

- Python 3.10+ → [python.org/downloads](https://python.org/downloads)
- VS Code → [code.visualstudio.com](https://code.visualstudio.com/)
- A free Visual Crossing API key → [visualcrossing.com](https://visualcrossing.com/) (used to fetch actual temperatures after market resolution)

## Installation and setup

Open VS Code, press **Ctrl + \`** to open the terminal and run:

```bash
pip install requests
```

Create a new folder called weatherbet. Inside it, create two files:

- [weatherbet.py](https://weatherbet.py/) ← the bot
- config.json ← all settings

The data/ folder will be created automatically on first run. It stores everything - market files, calibration data, bot state.

## Part 1: config.json - all settings in one place

```json
{
  "balance": 10000.0,
  "max_bet": 20.0,
  "min_ev": 0.05,
  "max_price": 0.45,
  "min_volume": 2000,
  "min_hours": 2.0,
  "max_hours": 72.0,
  "kelly_fraction": 0.25,
  "max_slippage": 0.03,
  "scan_interval": 3600,
  "calibration_min": 30,
  "vc_key": "YOUR_KEY_HERE"
}
```

In Part 1, all thresholds were hardcoded directly in the Python file - annoying to change. Now everything lives in config.json - no need to touch the code.

What each parameter does:

**balance:** starting virtual balance. The bot doesn't touch real money, but tracks every trade against this number so you can measure performance accurately.

**max\_bet:** maximum dollars per trade. Even if Kelly says "bet $200", the bot caps it at this number. Start small - $20 is enough to test the strategy without overexposing your balance to a single market.

**min\_ev:** minimum Expected Value to enter a trade. More on this in Part 4. Start with 0.05 and raise it once you have data.

**max\_price:** skip buckets above this price. At $0.45 the market has already priced in most of the edge. Upside is minimal.

**min\_volume:** skip markets with less liquidity. Thin markets have wide spreads and unreliable prices. I recommend $2000+.

**min\_hours / max\_hours:** only trade markets resolving within this window. Under 2 hours is too close to resolution. Over 72 hours means the forecast is too uncertain.

**kelly\_fraction:** how aggressive the Kelly sizing is. 0.25 means the bot uses 25% of the full Kelly recommendation. Full Kelly is mathematically optimal but aggressive in practice - fractional Kelly reduces variance.

**max\_slippage:** maximum acceptable spread between ask and bid. If the difference exceeds $0.03, skip that market. On thin markets the spread can reach 10 cents - not worth trading.

**scan\_interval:** seconds between full scans. 3600 = every hour.

**calibration\_min:** minimum resolved trades per city before calibration kicks in. More on this in Part 7.

**vc\_key:** Visual Crossing API key. Used to fetch actual temperatures after resolution so the bot can compare them with the forecast and calculate accuracy.

## Part 2: Locations - 20 cities, airport coordinates

```python
LOCATIONS = {
    # US — °F
    "nyc":          {"lat": 40.7772,  "lon":  -73.8726, "name": "New York City", "station": "KLGA", "unit": "F", "region": "us"},
    "chicago":      {"lat": 41.9742,  "lon":  -87.9073, "name": "Chicago",       "station": "KORD", "unit": "F", "region": "us"},
    "miami":        {"lat": 25.7959,  "lon":  -80.2870, "name": "Miami",         "station": "KMIA", "unit": "F", "region": "us"},
    "dallas":       {"lat": 32.8471,  "lon":  -96.8518, "name": "Dallas",        "station": "KDAL", "unit": "F", "region": "us"},
    "seattle":      {"lat": 47.4502,  "lon": -122.3088, "name": "Seattle",       "station": "KSEA", "unit": "F", "region": "us"},
    "atlanta":      {"lat": 33.6407,  "lon":  -84.4277, "name": "Atlanta",       "station": "KATL", "unit": "F", "region": "us"},
    # EU — °C
    "london":       {"lat": 51.5048,  "lon":    0.0495, "name": "London",        "station": "EGLC", "unit": "C", "region": "eu"},
    "paris":        {"lat": 48.9962,  "lon":    2.5979, "name": "Paris",         "station": "LFPG", "unit": "C", "region": "eu"},
    "munich":       {"lat": 48.3537,  "lon":   11.7750, "name": "Munich",        "station": "EDDM", "unit": "C", "region": "eu"},
    "ankara":       {"lat": 40.1281,  "lon":   32.9951, "name": "Ankara",        "station": "LTAC", "unit": "C", "region": "eu"},
    # Asia — °C
    "seoul":        {"lat": 37.4691,  "lon":  126.4505, "name": "Seoul",         "station": "RKSI", "unit": "C", "region": "asia"},
    "tokyo":        {"lat": 35.7647,  "lon":  140.3864, "name": "Tokyo",         "station": "RJTT", "unit": "C", "region": "asia"},
    "shanghai":     {"lat": 31.1443,  "lon":  121.8083, "name": "Shanghai",      "station": "ZSPD", "unit": "C", "region": "asia"},
    "singapore":    {"lat":  1.3502,  "lon":  103.9940, "name": "Singapore",     "station": "WSSS", "unit": "C", "region": "asia"},
    "lucknow":      {"lat": 26.7606,  "lon":   80.8893, "name": "Lucknow",       "station": "VILK", "unit": "C", "region": "asia"},
    "tel-aviv":     {"lat": 32.0114,  "lon":   34.8867, "name": "Tel Aviv",      "station": "LLBG", "unit": "C", "region": "asia"},
    # Americas — °C
    "toronto":      {"lat": 43.6772,  "lon":  -79.6306, "name": "Toronto",       "station": "CYYZ", "unit": "C", "region": "ca"},
    "sao-paulo":    {"lat": -23.4356, "lon":  -46.4731, "name": "Sao Paulo",     "station": "SBGR", "unit": "C", "region": "sa"},
    "buenos-aires": {"lat": -34.8222, "lon":  -58.5358, "name": "Buenos Aires",  "station": "SAEZ", "unit": "C", "region": "sa"},
    # Oceania — °C
    "wellington":   {"lat": -41.3272, "lon":  174.8052, "name": "Wellington",    "station": "NZWN", "unit": "C", "region": "oc"},
}
```

Part 1 only covered US cities in Fahrenheit. This version adds Europe, Asia, South America and Oceania - all markets currently active on Polymarket.

A few important things:

**Coordinates matter.** Every city points to a specific airport station - not the city center. Polymarket resolves its markets against Weather Underground data, which in turn pulls from these exact ICAO stations. Using city center coordinates can introduce a 3-8°F error before the algorithm even starts.

**Unit system per city.** US cities use Fahrenheit, everything else uses Celsius. The bot handles this automatically - forecasts are requested in the right units and all comparisons stay within the same scale.

**Region tag.** The region field tells the bot which forecast source to use. US cities get the HRRR model for D+0 and D+1. Everything else gets ECMWF. More on this in Part 3.

Weather markets for new cities continue to appear on Polymarket, and you can add them yourself. Find the city’s ICAO airport code (e.g., EGLL for Heathrow), retrieve the coordinates from any weather website, and add them to the dictionary. If Polymarket already has a market for that city, the bot will find it automatically.

## Part 3: Three forecast sources

This is the biggest upgrade from Part 1.

Part 1 used a single source - NWS - which only covers US cities. Now the bot uses three sources with different strengths and picks the best one for each city and time horizon.

**ECMWF via Open-Meteo:**

```python
def get_ecmwf(city_slug, dates):
    url = (
        f"https://api.open-meteo.com/v1/forecast"
        f"?latitude={loc['lat']}&longitude={loc['lon']}"
        f"&daily=temperature_2m_max&temperature_unit={temp_unit}"
        f"&forecast_days=7&timezone={TIMEZONES.get(city_slug, 'UTC')}"
        f"&models=ecmwf_ifs025&bias_correction=true"
    )
    data = requests.get(url, timeout=(5, 8)).json()
    # returns {date: temp} for each requested date
```

ECMWF (European Centre for Medium-Range Weather Forecasts) is the gold standard for global weather prediction. Covers all 20 cities, handles both °F and °C, forecasts up to 7 days out. Access via Open-Meteo is completely free, no API key is required.

The **bias\_correction=true** parameter matters. It applies a statistical correction based on historical errors for each location - this improves accuracy meaningfully.

ECMWF updates twice a day (around 6 UTC and 18 UTC), so there's no point querying it more often.

**HRRR via Open-Meteo (US only):**

```python
def get_hrrr(city_slug, dates):
    if LOCATIONS[city_slug]["region"] != "us":
        return {}
    url = (
        f"https://api.open-meteo.com/v1/forecast"
        f"?latitude={loc['lat']}&longitude={loc['lon']}"
        f"&daily=temperature_2m_max&temperature_unit=fahrenheit"
        f"&forecast_days=3&timezone={TIMEZONES.get(city_slug, 'UTC')}"
        f"&models=gfs_seamless"
    )
```

For US cities on D+0 and D+1, we use the GFS Seamless model - a combination of HRRR (high resolution, updates hourly) and GFS (global, longer horizon). It's significantly more accurate than ECMWF for short-range US forecasts because it runs at higher resolution and incorporates real-time observations.

The bot automatically picks HRRR over ECMWF for US cities when the horizon is within 48 hours.

**METAR - actual observations:**

```python
def get_metar(city_slug):
    station = LOCATIONS[city_slug]["station"]
    url = f"https://aviationweather.gov/api/data/metar?ids={station}&format=json"
    data = requests.get(url, timeout=(5, 8)).json()
    temp_c = data[0].get("temp")
    # convert to °F if needed and return
```

METAR is aviation weather data - real-time observations from the exact airport stations Polymarket uses to resolve its markets. Used only for D+0 to see what the temperature actually is right now.

If it's 2pm and the station already recorded 68°F, but the ECMWF forecast says 64°F - the forecast undershot. The bot logs this as an additional data point.

**Which source does the bot use?**

Simple logic: HRRR for US cities on D+0 and D+1, ECMWF for everything else. METAR is logged as additional data but doesn't change trading decisions on its own - that comes with calibration.

## Part 4: Expected Value and Kelly Criterion

This is the mathematical core of the bot. In Part 1, the signal was simple: if the market prices a bucket below 15¢, buy it. That's fine as a starting point, but it ignores how confident the forecast actually is. A 14¢ bucket where you're 90% sure beats a 5¢ bucket where you're 30% sure - but the old logic treated them the same.

**Expected Value:**

```python
def calc_ev(p, price):
    if price <= 0 or price >= 1: return 0.0
    return round(p * (1.0 / price - 1.0) - (1.0 - p), 4)
```

EV answers the question: "For every dollar I bet, how much do I expect to make?"

If the forecast lands in the 42-43°F bucket and the price is $0.14:

- Win: you get $1.00 back on a $0.14 bet → profit of $0.86
- Lose: you lose $0.14

With p = 0.80 (80% confidence): EV = 0.80 × (1/0.14 − 1) − 0.20 = **+4.94**

That means for every $1 bet, you expect to make $4.94 on average. Any EV above 0 is theoretically profitable. In practice, set min\_ev to filter out weak signals - start with 0.05 and raise it as you gather data.

**Kelly Criterion:**

```python
def calc_kelly(p, price):
    b = 1.0 / price - 1.0          # net odds
    f = (p * b - (1.0 - p)) / b    # Kelly fraction
    return min(max(0.0, f) * KELLY_FRACTION, 1.0)
```

Kelly answers the question: "How much of my balance should I bet?"

Formula: f = (p × b − (1 − p)) / b, where b is the net odds (what you win per dollar risked).

We multiply the result by kelly\_fraction (0.25 by default) to use fractional Kelly - this reduces variance while keeping the system profitable over time. We also hard-cap every bet at max\_bet from the config.

**A note on bid and ask.** The bot enters positions at the ask price (the real cost of entry) and monitors/closes at the bid price (what someone is willing to pay you). This matters for honest simulation - otherwise results will be inflated.

**Right now the bot uses p = 1.0** - it assumes the forecast is always correct. This is a placeholder until calibration kicks in. Once enough data is collected, p will be replaced with the actual historical accuracy of each forecast source per city.

## Part 5: Data storage - one file per market

Every market gets its own JSON file in **data/markets/**. For example, **data/markets/nyc\_2026-03-19.json** contains everything about that market from discovery to resolution.

```python
def new_market(city_slug, date_str, event, hours):
    return {
        "city":               city_slug,
        "city_name":          loc["name"],
        "date":               date_str,
        "status":             "open",
        "position":           None,
        "actual_temp":        None,
        "resolved_outcome":   None,
        "pnl":                None,
        "forecast_snapshots": [],   # every hourly forecast update
        "market_snapshots":   [],   # every price check
        "all_outcomes":       [],   # all buckets with prices
        "created_at":         datetime.now(timezone.utc).isoformat(),
    }
```

very hour when the bot scans, it appends a new entry to **forecast\_snapshots**:

```json
{
  "ts": "2026-03-18T11:28:58Z",
  "horizon": "D+1",
  "hours_left": 24.6,
  "ecmwf": 40,
  "hrrr": 42,
  "metar": null,
  "best": 42,
  "best_source": "hrrr"
}
```

This means you can see the full history of how the forecast evolved for each market - did it stay stable or jump around? Did the models agree or disagree?

After the market resolves, the bot queries the Polymarket API directly - checks if the market is closed and what the final YES price is.

- If YES price >= $0.95 - WIN
- If <= $0.05 - LOSS

This is more accurate than any external source because it's the actual market outcome.

## Part 6: The main loop

```python
def scan_and_update():
    for city_slug in LOCATIONS:
        # 1. Fetch forecasts from all sources
        snapshots = take_forecast_snapshot(city_slug, dates)

        for date in dates:
            # 2. Find the Polymarket event
            event = get_polymarket_event(city_slug, month, day, year)

            # 3. Load or create market record
            mkt = load_market(city_slug, date) or new_market(...)

            # 4. Save forecast snapshot
            mkt["forecast_snapshots"].append(forecast_snap)

            # 5. Check stops
            if position_open and price_dropped_20pct:
                close_position(stop_loss)

            # 6. Close if forecast moved significantly
            if position_open and forecast_moved_far:
                close_position(forecast_changed)

            # 7. Open position if signal found
            if no_position and good_ev and right_bucket:
                open_position(size)

            save_market(mkt)

    # 8. Auto-resolve closed markets
    auto_resolve_via_polymarket_api()
```

The loop runs every hour and does eight things in order.

First it pulls fresh forecasts from all three sources. Then for each city and date it finds the corresponding Polymarket event and loads the existing market record (or creates a new one). It saves the current forecast as a snapshot - this is your data for calibration.

Then it checks stops. The bot closes a position if:

- Price dropped 20% from entry (stop-loss)
- Price rose 20%+ then fell back to entry price (trailing stop at breakeven)
- Forecast moved significantly out of the bought bucket (2°F / 1°C buffer to avoid reacting to small hourly fluctuations)

If no position is open and the current forecast lands in an underpriced bucket with positive EV and acceptable spread - the bot opens a position sized by Kelly.

Between full scans, every 10 minutes a quick monitoring pass runs - only checking stops without scanning all cities.

## Part 7: Calibration

```python
def run_calibration(markets):
    resolved = [m for m in markets if m["status"] == "resolved"]
    for source in ["ecmwf", "hrrr", "metar"]:
        for city in all_cities:
            errors = [abs(snapshot_temp - actual_temp) for each resolved market]
            if len(errors) >= calibration_min:
                mae = sum(errors) / len(errors)
                calibration[f"{city}_{source}"] = {"sigma": mae}
```

Once the bot has at least 30 resolved markets per city, it calculates the mean absolute error (MAE) of each forecast source for that city. This MAE becomes sigma - the expected forecast error.

With a real sigma, the probability calculation changes. Instead of p = 1.0, the bot uses a normal distribution to estimate the probability that the actual temperature lands in a given bucket. A sigma of 2°F means the forecast is uncertain by roughly 2 degrees - the bot accounts for this when sizing bets.

This is why data collection matters. The more resolved markets you have, the more accurate your sigma estimates become, and the more accurately the bot sizes its trades.

You can track calibration progress in **data/calibration.json**. Once it starts filling up, you'll see which cities have reliable forecasts and which don't - and adjust your strategy accordingly.

## Running the bot

```bash
python weatherbet.py           # start the bot — scans every hour
python weatherbet.py status    # balance and open positions
python weatherbet.py report    # full breakdown of all resolved markets
```

Run it and watch a few scans complete:

![[003 Resources/Assets/a5f0c5cbc9c5edcc272058783cf1421c_MD5.png]]

![[003 Resources/Assets/ea0b5c14cb5ef85bd61fc9ee7305db4f_MD5.png]]

Leave it running for at least 2-3 weeks before drawing conclusions. Weather markets resolve daily - you need 50-100 resolved trades to get meaningful win rate data.

## What's next

The bot is now collecting real data. Once calibration kicks in, you'll be able to answer questions like: which cities have the most accurate forecasts? Which time horizons are worth trading? Which source is more reliable for Europe vs Asia?

That's the next step - calibration analysis and source comparison based on real data.

If you found this helpful, likes and bookmarks are greatly appreciated. Follow me to stay updated!

Full source code on GitHub: [github.com/alteregoeth-ai/weatherbot](https://github.com/alteregoeth-ai/weatherbot)