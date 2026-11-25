import type { pushRoutes } from './client'
import type { PushInfo } from './generated/views'
import { isIOS, keyBytes, pushState, sameKey, subscriptionBody, type PushState } from './pushSubscribe.ts'

// The browser side of Web Push (docs/contributing/web-ui.md "Service
// worker"): subscribing this device with the Desk's key, refreshing it on
// every start, unsubscribing. Loaded lazily, like deskWorker.ts, and for
// the same reason client.ts and notify.ts come in as arguments: `api` is
// client.ts's pushRoutes. Errors are Errors whose message the Notifications
// dialog shows.

type Api = typeof pushRoutes
type Categories = Record<string, boolean>

function supported(): boolean {
  return window.isSecureContext && 'serviceWorker' in navigator && 'PushManager' in window
}

// isStandalone: the Desk runs as the Home Screen app, the only place iOS
// pushes to.
function isStandalone(): boolean {
  return (navigator as { standalone?: boolean }).standalone === true || window.matchMedia('(display-mode: standalone)').matches
}

// registration is the active service worker's, null without one within
// 10 s: ready waits for ever when /sw.js failed to register.
async function registration(): Promise<ServiceWorkerRegistration | null> {
  if (!supported()) return null
  return Promise.race([navigator.serviceWorker.ready, new Promise<null>((r) => window.setTimeout(() => r(null), 10_000))])
}

async function current(): Promise<PushSubscription | null> {
  const reg = await registration()
  return reg ? reg.pushManager.getSubscription().catch(() => null) : null
}

// pushStatus: what the dialog offers, the Desk's push info and whether this
// device has a subscription. A failed GET is in-tab only.
export async function pushStatus(api: Api): Promise<{ state: PushState; info: PushInfo | null; subscribed: boolean }> {
  const info = await api.info().catch(() => null)
  const state = pushState({ info, supported: supported(), ios: isIOS(navigator.userAgent, navigator.maxTouchPoints), standalone: isStandalone() })
  return { state, info, subscribed: state === 'push' && (await current()) !== null }
}

async function subscribe(reg: ServiceWorkerRegistration, publicKey: string): Promise<PushSubscription> {
  try {
    return await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) })
  } catch {
    throw new Error('This browser did not subscribe to push notifications.')
  }
}

// subscribePush subscribes this device with the Desk's key and stores the
// subscription; when storing fails it unsubscribes again. The caller has
// asked for the permission (enableNotify).
export async function subscribePush(api: Api, info: PushInfo, categories: Categories): Promise<void> {
  const reg = await registration()
  if (!reg || !info.publicKey) throw new Error('Push notifications are not ready in this browser.')
  const sub = (await reg.pushManager.getSubscription()) ?? (await subscribe(reg, info.publicKey))
  try {
    await put(api, sub, info.publicKey, categories)
  } catch (e) {
    await (await reg.pushManager.getSubscription())?.unsubscribe().catch(() => false)
    throw e
  }
}

// refreshPush stores the subscription again, on every start and on a
// category change. A subscription made with another key (the Desk's key
// was rotated) is made again first. It answers whether there is one.
export async function refreshPush(api: Api, info: PushInfo, categories: Categories): Promise<boolean> {
  const sub = await current()
  if (!sub || !info.publicKey) return false
  await put(api, sub, info.publicKey, categories)
  return true
}

async function put(api: Api, sub: PushSubscription, publicKey: string, categories: Categories) {
  if (!sameKey(sub.options.applicationServerKey, publicKey)) {
    const old = sub.endpoint
    const reg = await registration()
    await sub.unsubscribe().catch(() => false)
    void api.remove(old).catch(() => undefined)
    if (!reg) throw new Error('Push notifications are not ready in this browser.')
    sub = await subscribe(reg, publicKey)
  }
  await api.put(subscriptionBody(sub.toJSON(), categories))
}

// startPush runs after sign-in: a subscribed device stores its subscription
// again (refreshPush) and sets notify.push, so its tab raises no
// notifications of its own. A failed refresh asks again whether one is
// left; without push the tab notifies as before.
export async function startPush(api: Api, notify: { push: boolean; categories: Categories }): Promise<void> {
  try {
    const { info, subscribed } = await pushStatus(api)
    if (!subscribed || !info) return
    notify.push = true
    notify.push = await refreshPush(api, info, notify.categories).catch(async () => (await pushStatus(api)).subscribed)
  } catch {
    // The next start tries again.
  }
}

// unsubscribePush unsubscribes this device and deletes its subscription on
// the Desk. A failed DELETE is left: the push service answers 410 for the
// endpoint from now on, and the Desk deletes it then.
export async function unsubscribePush(api: Api): Promise<void> {
  const sub = await current()
  if (!sub) return
  await sub.unsubscribe().catch(() => false)
  await api.remove(sub.endpoint).catch(() => undefined)
}
