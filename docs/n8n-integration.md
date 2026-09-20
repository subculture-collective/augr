# n8n integration

Augr posts structured alert, signal, and decision events to one configured n8n
webhook.

```dotenv
N8N_WEBHOOK_URL=https://n8n.example.com/webhook/augr-events
N8N_WEBHOOK_SECRET=replace-with-a-secret
```

When a secret is set, requests include it in `X-Webhook-Secret`. Configure the
n8n workflow to reject requests without the expected value.

The envelope contains an `event_type` (`alert`, `signal`, or `decision`),
severity, RFC3339 timestamp, optional strategy and pipeline-run IDs, and an
event-specific `data` object. `callback_url` is reserved and is not populated by
the built-in emitters.

Signal and decision events follow completed strategy-run persistence. Alert
events require the relevant `ALERT_*_CHANNELS` setting to contain `n8n`.
Normal and smoke runners both dispatch notifications; smoke mode is useful for
a deterministic transport check.

A typical n8n workflow validates the secret, switches on `event_type`, stores
the raw payload, and then routes a concise message to the desired incident or
review channel. Keep fan-out in n8n rather than duplicating webhook endpoints in
Augr.
