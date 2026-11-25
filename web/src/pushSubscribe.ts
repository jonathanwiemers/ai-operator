import type { PushInfo, PushSubscriptionRequest } from './generated/views'

// Pure helpers of Web Push on the Desk (push.ts is the browser side). No
// DOM, Vue or client.ts imports here: the service worker uses them too.

// What the Notifications dialog offers on this device: push, the hint to add
// the Desk to the Home Screen (iOS pushes only there), push unavailable on
// this Desk (the dialog says why), or in-tab notifications only.
export type PushState = 'push' | 'iosHint' | 'unavailable' | 'inTab'

// pushState picks the dialog's state. info: GET /api/v1/push, null when it
// failed; supported: the browser has PushManager and a service worker.
export function pushState({ info, supported, ios, standalone }: { info: PushInfo | null; supported: boolean; ios: boolean; standalone: boolean }): PushState {
  if (info && !info.available) return 'unavailable'
  if (!info?.publicKey) return 'inTab'
  if (ios && !standalone) return 'iosHint'
  return supported ? 'push' : 'inTab'
}

// isIOS: an iPhone, iPad or iPod, also an iPad that reports a Mac's user
// agent (it has touch points, a Mac has none).
export function isIOS(userAgent: string, maxTouchPoints: number): boolean {
  return /iPhone|iPad|iPod/.test(userAgent) || (/Macintosh/.test(userAgent) && maxTouchPoints > 1)
}

// keyBytes decodes a base64url key (the server's publicKey).
export function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const b64 = base64url.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4))
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

// sameKey says whether a subscription was made with publicKey; false
// without a key, so a subscription of unknown origin is made again.
export function sameKey(applicationServerKey: ArrayBuffer | null, publicKey: string): boolean {
  if (!applicationServerKey) return false
  const a = new Uint8Array(applicationServerKey)
  const b = keyBytes(publicKey)
  return a.length === b.length && a.every((x, i) => x === b[i])
}

// subscriptionBody is the PUT body of a subscription (its toJSON()) with the
// categories picked on this device. Without categories (the service worker
// cannot read them) the field is left out and the server keeps the stored ones.
export function subscriptionBody(sub: PushSubscriptionJSON, categories: Record<string, boolean> | undefined): PushSubscriptionRequest {
  const body: PushSubscriptionRequest = {
    endpoint: sub.endpoint ?? '',
    keys: { p256dh: sub.keys?.p256dh ?? '', auth: sub.keys?.auth ?? '' },
  }
  if (categories) body.categories = Object.keys(categories).filter((c) => categories[c])
  return body
}
