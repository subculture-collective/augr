export const WEBSOCKET_EVENT_TYPES = [
  'pipeline_start',
  'agent_decision',
  'debate_round',
  'signal',
  'order_submitted',
  'order_filled',
  'position_update',
  'circuit_breaker',
  'error',
  'pipeline_health',
  'polymarket_whale_trade',
  'polymarket_price_move',
  'polymarket_account_tracked',
] as const;

export type WebSocketEventType = (typeof WEBSOCKET_EVENT_TYPES)[number];
