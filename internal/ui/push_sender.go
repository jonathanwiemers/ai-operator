package ui

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/webpush"
)

// The push sender wakes on project and task changes (debounced) and on a
// ticker. In a cycle, the replica holding the store's advisory lock pushes
// to every subscription the new candidates of its stored principal, in its
// categories. A key is claimed in the ledger before it is sent and
// unclaimed when sending fails, so each device gets it at most once.

const (
	// pushTTL is how long a push service keeps a push for an offline device.
	pushTTL = 24 * time.Hour
	// maxPushPayload caps a push's JSON payload (the cut, pushPayload).
	maxPushPayload = 3000
	// maxPushFailures in a row delete a subscription.
	maxPushFailures = 10
)

// pushTiming tunes the sender; tests shorten it.
type pushTiming struct {
	// debounce is the quiet time after a change before a cycle runs,
	// interval the time between cycles without changes, timeout the limit
	// of one send.
	debounce, interval, timeout time.Duration
	client                      *http.Client
}

func defaultPushTiming() pushTiming {
	const timeout = 10 * time.Second
	return pushTiming{debounce: 2 * time.Second, interval: time.Minute, timeout: timeout,
		client: &http.Client{Timeout: timeout,
			// A redirect could lead past the allowed push hosts: a 3xx is the
			// answer, and it counts as a failure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// RunPushSender sends push notifications until ctx ends. A cycle runs only
// once synced reports the cache synced, with push on and the lock held.
func (s *Server) RunPushSender(ctx context.Context, synced func() bool) {
	changes, cancel := s.hub.Subscribe(func(e Event) bool { return e.Kind == "project" || e.Kind == "task" })
	defer cancel()
	tick := time.NewTicker(s.pushTiming.interval)
	defer tick.Stop()
	debounce := time.NewTimer(s.pushTiming.debounce)
	debounce.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			debounce.Reset(s.pushTiming.debounce)
			continue
		case <-debounce.C:
		case <-tick.C:
		}
		s.pushCycle(ctx, synced)
	}
}

// pushStats counts a cycle's work for its log line.
type pushStats struct {
	subscriptions, sent, failed, deleted, skipped int
}

// pushCycle runs one cycle if it may, and logs its counts, never an
// endpoint, a key or a send error.
func (s *Server) pushCycle(ctx context.Context, synced func() bool) pushStats {
	var stats pushStats
	if !synced() || s.pushOff() != "" {
		return stats
	}
	st := s.pushStoreOf()
	unlock, ok, err := st.TryPushLock(ctx)
	if err != nil {
		s.log.Error(err, "push cycle: lock")
		return stats
	}
	if !ok {
		return stats // another replica sends
	}
	defer unlock()
	err = s.sendPushes(ctx, st, time.Now(), &stats)
	if err != nil && ctx.Err() == nil {
		s.log.Error(err, "push cycle")
	}
	log := s.log
	if stats.sent+stats.failed+stats.deleted+stats.skipped == 0 {
		log = log.V(1)
	}
	log.Info("push cycle", "subscriptions", stats.subscriptions, "sent", stats.sent, "failed", stats.failed,
		"deleted", stats.deleted, "skipped", stats.skipped)
	return stats
}

// sendPushes deletes what expired and pushes to every subscription in turn.
// The error of one subscription does not stop the others.
func (s *Server) sendPushes(ctx context.Context, st pushStore, now time.Time, stats *pushStats) error {
	expired, _, err := st.DeleteExpiredPush(ctx, now)
	if err != nil {
		return err
	}
	stats.deleted += int(expired)
	key, _, err := pushKey(ctx, st)
	if err != nil {
		return err
	}
	subs, err := st.PushSubscriptions(ctx)
	if err != nil {
		return err
	}
	stats.subscriptions = len(subs)
	var errs []error
	for _, sub := range subs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errs = append(errs, s.pushTo(ctx, st, sub, key, now, stats))
	}
	return errors.Join(errs...)
}

// pushTo sends sub's new candidates. Each key is claimed right before its
// send, so a cycle that stops early leaves no claim it did not send.
func (s *Server) pushTo(ctx context.Context, st pushStore, sub store.PushSubscription, key webpush.Key, now time.Time, stats *pushStats) error {
	cands, err := s.pushCandidates(ctx, st, Principal{Username: sub.Username, Roles: sub.Roles}, now)
	if err != nil {
		return err
	}
	var inbox []string
	for _, c := range cands {
		if c.Item != nil {
			inbox = append(inbox, c.Key)
		}
	}
	if err := st.ForgetPush(ctx, sub.ID, inbox); err != nil {
		return err
	}
	to := webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}
	for _, c := range cands {
		if !slices.Contains(sub.Categories, c.Category) {
			continue
		}
		claimed, err := st.ClaimPush(ctx, sub.ID, []string{c.Key}, now)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			continue // sent before
		}
		payload, err := pushPayload(c)
		if err != nil {
			// Too big even when cut: it stays claimed, it would never fit.
			stats.skipped++
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, s.pushTiming.timeout)
		code, err := webpush.Send(sendCtx, s.pushTiming.client, to, key, s.cfg.ExternalURL, payload,
			webpush.Message{TTL: pushTTL, Urgency: pushUrgency(c.Category)})
		cancel()
		switch {
		case err == nil && code >= 200 && code < 300:
			stats.sent++
			if err := st.PushSucceeded(ctx, sub.ID); err != nil {
				return err
			}
		case err == nil && (code == http.StatusNotFound || code == http.StatusGone):
			// The device unsubscribed or the subscription expired.
			stats.deleted++
			return st.DeletePushSubscriptionID(ctx, sub.ID)
		default:
			return s.pushFailed(ctx, st, sub.ID, c.Key, stats)
		}
	}
	return nil
}

// pushFailed releases the claim of key, so the next cycle sends it again,
// and counts the failure unless the cycle was cancelled. The subscription
// gets nothing more in this cycle.
func (s *Server) pushFailed(ctx context.Context, st pushStore, id int64, key string, stats *pushStats) error {
	if err := st.UnclaimPush(context.WithoutCancel(ctx), id, key); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	stats.failed++
	deleted, err := st.PushFailed(ctx, id, maxPushFailures)
	if deleted {
		stats.deleted++
	}
	return err
}

// pushUrgency is the Urgency of a push in category.
func pushUrgency(category string) string {
	if category == "problem" || category == "question" {
		return "high"
	}
	return "normal"
}

// pushMessage is a push's payload: an Inbox item or an event, which the
// service worker turns into a notification.
type pushMessage struct {
	Item  *InboxItem   `json:"item,omitempty"`
	Event *StreamEvent `json:"event,omitempty"`
}

var errPushTooBig = errors.New("push payload too big")

// pushPayload is the JSON of c, at most maxPushPayload bytes: the item's
// reason (an event's data.reason) is cut first, then its title.
func pushPayload(c pushCandidate) ([]byte, error) {
	if c.Item != nil {
		it := *c.Item
		return fitPushPayload(func() ([]byte, error) { return json.Marshal(pushMessage{Item: &it}) }, &it.Reason, &it.Title)
	}
	ev := *c.Event
	ev.Data = maps.Clone(ev.Data)
	reason, hasReason := ev.Data["reason"].(string)
	title, hasTitle := ev.Data["title"].(string)
	return fitPushPayload(func() ([]byte, error) {
		if hasReason {
			ev.Data["reason"] = reason
		}
		if hasTitle {
			ev.Data["title"] = title
		}
		return json.Marshal(pushMessage{Event: &ev})
	}, &reason, &title)
}

// fitPushPayload marshals, cutting texts in turn by runes with an ellipsis
// until the JSON fits maxPushPayload.
func fitPushPayload(marshal func() ([]byte, error), texts ...*string) ([]byte, error) {
	b, err := marshal()
	for _, t := range texts {
		if err != nil || len(b) <= maxPushPayload {
			break
		}
		full := *t
		// keep is the most runes of full (with the ellipsis) that fit.
		keep := sort.Search(utf8.RuneCountInString(full), func(n int) bool {
			*t = cutRunes(full, n+1)
			b, err = marshal()
			return err != nil || len(b) > maxPushPayload
		})
		*t = cutRunes(full, keep)
		b, err = marshal()
	}
	if err == nil && len(b) > maxPushPayload {
		err = errPushTooBig
	}
	return b, err
}

// cutRunes is s cut to n runes, the last an ellipsis; empty for n 0.
func cutRunes(s string, n int) string {
	if n == 0 {
		return ""
	}
	return truncateRunes(s, n)
}
