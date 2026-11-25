# Push API

This page lists the three routes a device uses to get
[push notifications](notifications.md#push) from the
[Desk](../concepts/glossary.md#desk): ask whether push is on, store a
[push subscription](../concepts/glossary.md#push-subscription), and delete
it. The Desk's browser code calls them; you need them only to write your own
client. The other routes are in the [Desk API](desk-api.md).

All three need a login, like the rest of the Desk API.

## Example

Ask whether push is on, then store a device's subscription with the
categories it picked:

```sh
curl -s -b "$COOKIE" "$DESK/api/v1/push"
```

```json
{"available": true, "publicKey": "BKq3…"}
```

```sh
curl -s -b "$COOKIE" -X PUT -H 'Content-Type: application/json' \
  -d '{"endpoint": "https://fcm.googleapis.com/fcm/send/abc",
       "keys": {"p256dh": "BNc…", "auth": "tBH…"},
       "categories": ["problem", "question", "release"]}' \
  "$DESK/api/v1/push/subscription"
```

The answer is `204` with no body. Without the database, `GET` answers:

```json
{"available": false, "reason": "Push notifications need the Desk's database (database.host)."}
```

## Routes

| Method and path | Purpose |
|-----------------|---------|
| `GET /api/v1/push` | `{ available, reason, publicKey }`. With `available`, `publicKey` is the key (base64url) a device subscribes with. Without it, `reason` says why push is off. |
| `PUT /api/v1/push/subscription` | Store the device's subscription with you as its user and your roles. `{ endpoint, keys: { p256dh, auth }, categories }`: the device's `PushSubscription` and the categories picked on it. |
| `DELETE /api/v1/push/subscription` | Delete the subscription of `{ endpoint }`, only if it is yours. |

- `categories` are the names of the [categories](notifications.md#categories):
  `problem`, `question`, `proposal`, `companion`, `budget`, `failed`,
  `milestoneStarted`, `milestoneDone` and `release`. A new subscription
  without `categories` gets all but `failed`; an existing one keeps its
  categories. An empty list picks none.
- The first `PUT` of an endpoint marks everything current as sent, so only
  newer things push. Every later `PUT` refreshes the subscription's 30 days.
- The Desk sends a `PUT` on every start while you are signed in.

## Status codes

| Code | When |
|------|------|
| `204` | `PUT` or `DELETE` worked. |
| `400` | The body is not valid JSON, the endpoint is not `https` on an [allowed push service](notifications.md#push), a key has the wrong length, or a category is unknown. `DELETE` without an `endpoint` is a `400` too. |
| `404` | `DELETE` of an endpoint that is not stored for you. Another user's subscription answers the same. |
| `409` | Push is off (`PUT` and `DELETE`; the body is the reason), or the endpoint is stored for another user: unsubscribe on the device and subscribe again. |

## Next

- [Notifications reference](notifications.md#push): when push is off, the
  expiry, the allowed push services.
- [Desk API](desk-api.md): the other routes.
- [Notifications guide](../guides/notifications.md): turn push on in the Desk.
