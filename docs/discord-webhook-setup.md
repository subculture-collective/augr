# Discord webhooks

Augr can send trading signals, stored agent decisions, and routed alerts to
separate Discord webhooks.

```dotenv
NOTIFY_DISCORD_SIGNAL_WEBHOOK_URL=https://discord.com/api/webhooks/...
NOTIFY_DISCORD_DECISION_WEBHOOK_URL=https://discord.com/api/webhooks/...
NOTIFY_DISCORD_ALERT_WEBHOOK_URL=https://discord.com/api/webhooks/...
```

Legacy `DISCORD_*` aliases still load for deployment compatibility, but new
configuration should use the names above.

- Signal notifications are emitted after a strategy run produces a final
  signal.
- Decision notifications are emitted for the stored agent decisions associated
  with the run.
- Alerts are sent only when the matching `ALERT_*_CHANNELS` setting contains
  `discord`; configuring the webhook alone does not opt an alert class in.

Normal and smoke strategy runners both support notification dispatch. Smoke
mode is deterministic; other environments may invoke external providers.

Discord delivery retries HTTP 429 responses using the server's retry delay, up
to the notifier's bounded attempt limit. Keep webhook URLs in the deployment's
secret store and test them with a paper-only run or a non-critical alert.
