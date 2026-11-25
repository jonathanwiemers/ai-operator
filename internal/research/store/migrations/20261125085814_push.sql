-- Web Push of the Desk. The UI server writes these tables as the reader
-- login, which may write them and no other table; research writers have no
-- access. push_keys holds the one VAPID key pair (the boolean id keeps it to
-- one row; deleting it rotates the key). push_subscriptions holds a device's
-- subscription with the principal of its last refresh; one not refreshed
-- for 30 days is deleted. push_sent is the ledger of what was pushed to a
-- subscription: a key is claimed before it is sent, and rows older than 30
-- days are deleted.

CREATE TABLE push_keys (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    public_key  bytea NOT NULL,
    private_key bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE push_subscriptions (
    id           bigserial PRIMARY KEY,
    endpoint     text NOT NULL UNIQUE,
    p256dh       text NOT NULL,
    auth         text NOT NULL,
    username     text NOT NULL,
    roles        text[] NOT NULL DEFAULT '{}',
    categories   text[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL,
    refreshed_at timestamptz NOT NULL,
    failures     int NOT NULL DEFAULT 0
);
CREATE INDEX push_subscriptions_refreshed_at_idx ON push_subscriptions (refreshed_at);

CREATE TABLE push_sent (
    subscription_id bigint NOT NULL REFERENCES push_subscriptions (id) ON DELETE CASCADE,
    key             text NOT NULL,
    sent_at         timestamptz NOT NULL,
    PRIMARY KEY (subscription_id, key)
);
CREATE INDEX push_sent_sent_at_idx ON push_sent (sent_at);
