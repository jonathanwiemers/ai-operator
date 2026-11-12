// Streams end at the session's short expiry and renew on reconnect, so a
// reconnect that comes back quickly is not shown as a lost connection.

// quietReconnectMs is how long a stream may be down before the Desk shows it.
export const quietReconnectMs = 5000

// quickReconnectEveryMs limits immediate reconnects, so a server that ends
// every stream at once falls back to the browser's retry and the backoff.
export const quickReconnectEveryMs = 60000

// reconnectAction says what follows an event stream error: 'now' reopens at
// once (the first error after an open), 'browser' leaves it to the
// EventSource's own retry, 'backoff' retries later (the source has given up).
export function reconnectAction(
  openedSinceError: boolean,
  closed: boolean,
  lastQuickAt: number,
  now: number,
): 'now' | 'browser' | 'backoff' {
  if (openedSinceError && now - lastQuickAt >= quickReconnectEveryMs) return 'now'
  return closed ? 'backoff' : 'browser'
}

// isRenewalEnd says whether a transcript stream's end reason only means the
// session needs renewing, which the reopened stream does.
export function isRenewalEnd(reason: string | undefined): boolean {
  return reason === 'session expired'
}
