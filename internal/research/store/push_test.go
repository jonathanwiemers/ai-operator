package store_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

var pushNow = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

func pushSub(endpoint, user string, categories ...string) store.PushSubscription {
	return store.PushSubscription{
		Endpoint: endpoint, P256dh: "p256dh-" + user, Auth: "auth-" + user, Username: user,
		Roles: []string{"dev"}, Categories: categories,
	}
}

func upsertPush(t *testing.T, s *store.Store, sub store.PushSubscription, keep bool, now time.Time) (int64, bool) {
	t.Helper()
	id, created, err := s.UpsertPushSubscription(t.Context(), sub, keep, now)
	if err != nil {
		t.Fatalf("UpsertPushSubscription = %v", err)
	}
	return id, created
}

func pushSubs(t *testing.T, s *store.Store) []store.PushSubscription {
	t.Helper()
	subs, err := s.PushSubscriptions(t.Context())
	if err != nil {
		t.Fatalf("PushSubscriptions = %v", err)
	}
	return subs
}

func claim(t *testing.T, s *store.Store, id int64, now time.Time, keys ...string) []string {
	t.Helper()
	got, err := s.ClaimPush(t.Context(), id, keys, now)
	if err != nil {
		t.Fatalf("ClaimPush(%v) = %v", keys, err)
	}
	return got
}

func wantKeys(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) || !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// A second upsert of an endpoint updates the principal and refreshed_at and
// keeps the id; keepCategories keeps the stored categories.
func TestUpsertPushSubscription(t *testing.T) {
	s := storetest.New(t)
	id, created := upsertPush(t, s, pushSub("https://fcm.googleapis.com/a", "ann", "problem"), false, pushNow)
	if !created {
		t.Error("first upsert: created = false")
	}
	other, _ := upsertPush(t, s, store.PushSubscription{Endpoint: "https://fcm.googleapis.com/b", Username: "bob"}, false, pushNow)

	later := pushNow.Add(time.Hour)
	sub := pushSub("https://fcm.googleapis.com/a", "ann2", "question")
	sub.Roles, sub.P256dh = []string{"ops", "dev"}, "new-p256dh"
	again, created := upsertPush(t, s, sub, false, later)
	if again != id || created {
		t.Errorf("second upsert = (%d, %v), want (%d, false)", again, created, id)
	}
	sub.Categories = []string{"release"}
	if again, created = upsertPush(t, s, sub, true, later.Add(time.Minute)); again != id || created {
		t.Errorf("upsert keeping categories = (%d, %v), want (%d, false)", again, created, id)
	}

	subs := pushSubs(t, s)
	if len(subs) != 2 || subs[0].ID != id || subs[1].ID != other {
		t.Fatalf("subscriptions = %+v, want ids %d and %d", subs, id, other)
	}
	got := subs[0]
	if got.Username != "ann2" || !slices.Equal(got.Roles, []string{"ops", "dev"}) || got.P256dh != "new-p256dh" ||
		got.Auth != "auth-ann2" || !slices.Equal(got.Categories, []string{"question"}) ||
		!got.CreatedAt.Equal(pushNow) || !got.RefreshedAt.Equal(later.Add(time.Minute)) || got.Failures != 0 {
		t.Errorf("subscription = %+v", got)
	}
	if b := subs[1]; b.Roles == nil || len(b.Roles) != 0 || len(b.Categories) != 0 {
		t.Errorf("subscription without roles and categories = %+v, want empty lists", b)
	}

	// A new row stores its categories, also with keepCategories.
	if _, created := upsertPush(t, s, pushSub("https://fcm.googleapis.com/c", "cy", "failed"), true, pushNow); !created {
		t.Error("new row with keepCategories: created = false")
	}
	if c := pushSubs(t, s)[2]; !slices.Equal(c.Categories, []string{"failed"}) {
		t.Errorf("new row with keepCategories has categories %v, want [failed]", c.Categories)
	}
}

// Deleting by endpoint needs the subscription's username.
func TestDeletePushSubscription(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	upsertPush(t, s, pushSub("https://fcm.googleapis.com/a", "ann"), false, pushNow)
	if ok, err := s.DeletePushSubscription(ctx, "https://fcm.googleapis.com/a", "bob"); err != nil || ok {
		t.Errorf("delete by another user = (%v, %v), want (false, nil)", ok, err)
	}
	if ok, err := s.DeletePushSubscription(ctx, "https://fcm.googleapis.com/other", "ann"); err != nil || ok {
		t.Errorf("delete of an unknown endpoint = (%v, %v), want (false, nil)", ok, err)
	}
	if n := len(pushSubs(t, s)); n != 1 {
		t.Fatalf("%d subscriptions after deletes by others, want 1", n)
	}
	if ok, err := s.DeletePushSubscription(ctx, "https://fcm.googleapis.com/a", "ann"); err != nil || !ok {
		t.Errorf("delete by its user = (%v, %v), want (true, nil)", ok, err)
	}
	if n := len(pushSubs(t, s)); n != 0 {
		t.Errorf("%d subscriptions after the delete, want 0", n)
	}
}

// A key is claimed once until unclaimed; deleting a subscription deletes its
// ledger; seeding with a list claims only the keys not claimed yet.
func TestClaimPush(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	a, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/a", "ann"), false, pushNow)
	b, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/b", "bob"), false, pushNow)

	wantKeys(t, "first claim", claim(t, s, a, pushNow, "problem/1"), "problem/1")
	wantKeys(t, "second claim", claim(t, s, a, pushNow, "problem/1"))
	wantKeys(t, "claim for another subscription", claim(t, s, b, pushNow, "problem/1"), "problem/1")
	if err := s.UnclaimPush(ctx, a, "problem/1"); err != nil {
		t.Fatal(err)
	}
	wantKeys(t, "claim after unclaim", claim(t, s, a, pushNow, "problem/1"), "problem/1")
	wantKeys(t, "empty claim", claim(t, s, a, pushNow))

	// Seeding, then an overlapping list.
	wantKeys(t, "seed", claim(t, s, b, pushNow, "question/2", "event/r1", "question/2"), "event/r1", "question/2")
	wantKeys(t, "overlapping claim", claim(t, s, b, pushNow, "problem/1", "question/3", "event/r1", "event/r2"),
		"event/r2", "question/3")

	if err := s.DeletePushSubscriptionID(ctx, b); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.Pool(), "push_sent"); n != 1 {
		t.Errorf("push_sent has %d rows after deleting a subscription, want 1 (the other's)", n)
	}
	if _, err := s.ClaimPush(ctx, b, []string{"problem/9"}, pushNow); err == nil {
		t.Error("claim for a deleted subscription = nil, want an error")
	}
}

// ForgetPush keeps event keys and the kept keys, and only of its
// subscription.
func TestForgetPush(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	a, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/a", "ann"), false, pushNow)
	b, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/b", "bob"), false, pushNow)
	claim(t, s, a, pushNow, "problem/1", "question/2", "event/r1", "eventual/3")
	claim(t, s, b, pushNow, "problem/1")
	if err := s.ForgetPush(ctx, a, []string{"question/2"}); err != nil {
		t.Fatal(err)
	}
	wantKeys(t, "claim after forget", claim(t, s, a, pushNow, "problem/1", "question/2", "event/r1", "eventual/3"),
		"eventual/3", "problem/1")
	wantKeys(t, "other subscription", claim(t, s, b, pushNow, "problem/1"))

	// Without keep, every non-event key goes.
	if err := s.ForgetPush(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	wantKeys(t, "claim after forget without keep", claim(t, s, a, pushNow, "problem/1", "question/2", "event/r1"),
		"problem/1", "question/2")
}

// PushFailed deletes the subscription at max failures in a row, and
// PushSucceeded resets the count.
func TestPushFailures(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	id, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/a", "ann"), false, pushNow)
	fail := func(want bool) {
		t.Helper()
		if deleted, err := s.PushFailed(ctx, id, 3); err != nil || deleted != want {
			t.Fatalf("PushFailed = (%v, %v), want (%v, nil)", deleted, err, want)
		}
	}
	fail(false)
	fail(false)
	if f := pushSubs(t, s)[0].Failures; f != 2 {
		t.Errorf("failures = %d, want 2", f)
	}
	if err := s.PushSucceeded(ctx, id); err != nil {
		t.Fatal(err)
	}
	if f := pushSubs(t, s)[0].Failures; f != 0 {
		t.Errorf("failures after a success = %d, want 0", f)
	}
	fail(false)
	fail(false)
	claim(t, s, id, pushNow, "problem/1")
	fail(true)
	if n := len(pushSubs(t, s)); n != 0 {
		t.Errorf("%d subscriptions after max failures, want 0", n)
	}
	if n := count(t, s.Pool(), "push_sent"); n != 0 {
		t.Errorf("push_sent has %d rows of the deleted subscription, want 0", n)
	}
	// A subscription that is gone is not an error.
	fail(false)
	if err := s.PushSucceeded(ctx, id); err != nil {
		t.Errorf("PushSucceeded of a deleted subscription = %v", err)
	}
}

// DeleteExpiredPush deletes subscriptions not refreshed and ledger rows not
// sent within PushExpiry, and keeps newer ones.
func TestDeleteExpiredPush(t *testing.T) {
	s := storetest.New(t)
	old := pushNow.Add(-store.PushExpiry - time.Minute)
	recent := pushNow.Add(-store.PushExpiry + time.Minute)
	stale, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/stale", "ann"), false, old)
	fresh, _ := upsertPush(t, s, pushSub("https://fcm.googleapis.com/fresh", "ann"), false, old)
	upsertPush(t, s, pushSub("https://fcm.googleapis.com/fresh", "ann"), false, recent) // refreshed
	claim(t, s, stale, recent, "problem/1")
	claim(t, s, fresh, old, "problem/old", "event/old")
	claim(t, s, fresh, recent, "problem/new")

	subs, sent, err := s.DeleteExpiredPush(t.Context(), pushNow)
	if err != nil || subs != 1 || sent != 2 {
		t.Fatalf("DeleteExpiredPush = (%d, %d, %v), want (1, 2, nil)", subs, sent, err)
	}
	if got := pushSubs(t, s); len(got) != 1 || got[0].ID != fresh {
		t.Errorf("subscriptions = %+v, want only %d", got, fresh)
	}
	wantKeys(t, "claim after expiry", claim(t, s, fresh, pushNow, "problem/old", "event/old", "problem/new"),
		"event/old", "problem/old")
}

// EnsurePushKey keeps the first key; PushKey reports none before.
func TestEnsurePushKey(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	if _, ok, err := s.PushKey(ctx); err != nil || ok {
		t.Fatalf("PushKey before = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	first, err := s.EnsurePushKey(ctx, []byte{4, 1}, []byte{1})
	if err != nil || !bytes.Equal(first.Public, []byte{4, 1}) || !bytes.Equal(first.Private, []byte{1}) || first.CreatedAt.IsZero() {
		t.Fatalf("EnsurePushKey = (%+v, %v)", first, err)
	}
	second, err := s.EnsurePushKey(ctx, []byte{4, 2}, []byte{2})
	if err != nil || !bytes.Equal(second.Public, first.Public) || !bytes.Equal(second.Private, first.Private) {
		t.Errorf("second EnsurePushKey = (%+v, %v), want the first key", second, err)
	}
	if k, ok, err := s.PushKey(ctx); err != nil || !ok || !bytes.Equal(k.Private, first.Private) {
		t.Errorf("PushKey = (%+v, %v, %v), want the first key", k, ok, err)
	}
}

// Only one holder of the push lock at a time; unlock frees it.
func TestTryPushLock(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	unlock, ok, err := s.TryPushLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first TryPushLock = (%v, %v), want (true, nil)", ok, err)
	}
	if again, ok, err := s.TryPushLock(ctx); err != nil || ok || again != nil {
		t.Fatalf("TryPushLock while held = (%v, %v), want (false, nil) and no unlock", ok, err)
	}
	unlock()
	unlock() // a second call is a no-op
	next, ok, err := s.TryPushLock(ctx)
	if err != nil || !ok {
		t.Fatalf("TryPushLock after unlock = (%v, %v), want (true, nil)", ok, err)
	}
	next()
}
