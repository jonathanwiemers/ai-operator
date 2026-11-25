package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/webpush"
)

// PushInfo says whether this Desk sends push notifications: with Available
// the device may subscribe with PublicKey (the applicationServerKey,
// base64url), otherwise Reason says what is missing.
type PushInfo struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
}

// pushStore is what the push routes need of the reader's store.
type pushStore interface {
	PushKey(ctx context.Context) (store.PushKey, bool, error)
	EnsurePushKey(ctx context.Context, public, private []byte) (store.PushKey, error)
	UpsertPushSubscription(ctx context.Context, sub store.PushSubscription, keepCategories bool, now time.Time) (int64, bool, error)
	DeletePushSubscription(ctx context.Context, endpoint, username string) (bool, error)
	DeletePushSubscriptionID(ctx context.Context, id int64) error
	ClaimPush(ctx context.Context, subID int64, keys []string, now time.Time) ([]string, error)
	Stream(ctx context.Context, q store.StreamQuery) ([]store.Event, error)

	// The sender's (push_sender.go).
	PushSubscriptions(ctx context.Context) ([]store.PushSubscription, error)
	UnclaimPush(ctx context.Context, subID int64, key string) error
	ForgetPush(ctx context.Context, subID int64, keep []string) error
	PushFailed(ctx context.Context, id int64, max int) (bool, error)
	PushSucceeded(ctx context.Context, id int64) error
	DeleteExpiredPush(ctx context.Context, now time.Time) (subs, sent int64, err error)
	TryPushLock(ctx context.Context) (unlock func(), ok bool, err error)
}

// pushStoreOf is the store of the push routes, nil until connected.
func (s *Server) pushStoreOf() pushStore {
	if s.pushes != nil {
		return s.pushes
	}
	if st := s.research.Load(); st != nil {
		return st
	}
	return nil
}

// pushOff is why push is off on this Desk, empty when it is on. The first
// reason that applies is the one an admin acts on.
func (s *Server) pushOff() string {
	switch {
	case !s.cfg.WebPush.On():
		return "Push notifications are turned off on this Desk (ui.webPush.enabled)."
	case !s.cfg.Research.Enabled():
		return "Push notifications need the Desk's database (database.host)."
	case s.cfg.ExternalURL == "":
		return "Push notifications need the Desk's external URL (ui.externalURL)."
	case s.pushStoreOf() == nil:
		return "The Desk's database is not connected yet."
	}
	return ""
}

// pushKey reads the VAPID key, and generates and stores one when there is
// none; generated reports that this call stored it. It is read on every
// call, not cached: deleting the row rotates the key on all replicas.
func pushKey(ctx context.Context, st pushStore) (key webpush.Key, generated bool, err error) {
	stored, ok, err := st.PushKey(ctx)
	if err != nil {
		return webpush.Key{}, false, err
	}
	if !ok {
		fresh, err := webpush.GenerateKey()
		if err != nil {
			return webpush.Key{}, false, err
		}
		// Another replica may store its key first: EnsurePushKey returns
		// the one stored.
		if stored, err = st.EnsurePushKey(ctx, fresh.Public(), fresh.Private()); err != nil {
			return webpush.Key{}, false, err
		}
		generated = string(stored.Public) == string(fresh.Public())
	}
	key, err = webpush.ParseKey(stored.Public, stored.Private)
	if err != nil {
		return webpush.Key{}, false, fmt.Errorf("stored push key: %w", err)
	}
	return key, generated, nil
}

// preparePushKey makes sure the key exists once the store is connected, so
// it is there before the first Desk asks.
func (s *Server) preparePushKey(ctx context.Context) {
	if s.pushOff() != "" {
		return
	}
	_, generated, err := pushKey(ctx, s.pushStoreOf())
	switch {
	case err != nil:
		s.log.Error(err, "push key not ready")
	case generated:
		s.log.Info("push key generated")
	default:
		s.log.Info("push key ready")
	}
}

// pushInfo answers GET /api/v1/push.
func (s *Server) pushInfo(w http.ResponseWriter, r *http.Request) {
	if reason := s.pushOff(); reason != "" {
		writeJSON(w, http.StatusOK, PushInfo{Reason: reason})
		return
	}
	key, _, err := pushKey(r.Context(), s.pushStoreOf())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PushInfo{Available: true, PublicKey: key.PublicBase64()})
}

// pushCategories are the categories a device may pick, as in the Desk's
// notification settings; all but failed are on by default.
var pushCategories = []string{"problem", "question", "proposal", "companion", "budget", "failed",
	"milestoneStarted", "milestoneDone", "release"}

func defaultPushCategories() []string {
	return slices.DeleteFunc(slices.Clone(pushCategories), func(c string) bool { return c == "failed" })
}

// maxPushBody caps the push routes' request bodies.
const maxPushBody = 16 << 10

// PushSubscriptionRequest is the body of PUT /api/v1/push/subscription: the
// device's PushSubscription.toJSON() and the categories picked on it.
type PushSubscriptionRequest struct {
	Endpoint string               `json:"endpoint"`
	Keys     PushSubscriptionKeys `json:"keys"`
	// Categories are the picked categories. Absent keeps the stored ones,
	// or the defaults on a new subscription; empty picks none.
	Categories *[]string `json:"categories,omitempty"`
}

// PushSubscriptionKeys are a subscription's keys, base64url.
type PushSubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// PushUnsubscribeRequest is the body of DELETE /api/v1/push/subscription.
type PushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// allowedPushEndpoint reports whether endpoint is an https URL on one of
// hosts, without user info or a port other than 443. A host "*.name"
// matches the subdomains of name, not name itself.
func allowedPushEndpoint(endpoint string, hosts []string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range hosts {
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if sub, ok := strings.CutSuffix(host, "."+suffix); ok && sub != "" && !strings.HasSuffix(sub, ".") {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// pushKeyLen checks a base64url subscription key's decoded length.
func pushKeyLen(s string, n int) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	return err == nil && len(b) == n
}

// pushRequestError is what is wrong with req, empty when nothing is.
func (s *Server) pushRequestError(req PushSubscriptionRequest) string {
	switch {
	case !allowedPushEndpoint(req.Endpoint, s.cfg.WebPush.AllowedHosts):
		return "endpoint is not an https URL of an allowed push service (ui.webPush.allowedHosts)"
	case !pushKeyLen(req.Keys.P256dh, 65) || !pushKeyLen(req.Keys.Auth, 16):
		return "keys.p256dh and keys.auth must be the subscription's base64url keys"
	}
	if req.Categories != nil {
		for _, c := range *req.Categories {
			if !slices.Contains(pushCategories, c) {
				return fmt.Sprintf("unknown category %q", c)
			}
		}
	}
	return ""
}

// putPushSubscription answers PUT /api/v1/push/subscription: it stores the
// device's subscription with the caller as its principal and, the first
// time, seeds its ledger with what is current, so only newer things push.
func (s *Server) putPushSubscription(w http.ResponseWriter, r *http.Request) {
	if reason := s.pushOff(); reason != "" {
		writeError(w, http.StatusConflict, reason)
		return
	}
	var req PushSubscriptionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := s.pushRequestError(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	p, st, now := principalOf(r), s.pushStoreOf(), time.Now()
	sub := store.PushSubscription{Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth,
		Username: p.Username, Roles: p.Roles, Categories: defaultPushCategories()}
	if req.Categories != nil {
		sub.Categories = slices.Compact(slices.Sorted(slices.Values(*req.Categories)))
	}
	id, created, err := st.UpsertPushSubscription(r.Context(), sub, req.Categories == nil, now)
	if errors.Is(err, store.ErrPushSubscriptionTaken) {
		writeError(w, http.StatusConflict, "this push subscription belongs to another user: unsubscribe on the device and subscribe again")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if created {
		if err := s.seedPush(r.Context(), st, id, p, now); err != nil {
			// Without its ledger the subscription would get the backlog:
			// drop it, the device's next PUT creates it again.
			if derr := st.DeletePushSubscriptionID(context.WithoutCancel(r.Context()), id); derr != nil {
				err = errors.Join(err, derr)
			}
			s.internalError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// seedPush claims every current candidate of p for subscription id, in all
// categories: turning one on later pushes only what is new.
func (s *Server) seedPush(ctx context.Context, st pushStore, id int64, p Principal, now time.Time) error {
	cands, err := s.pushCandidates(ctx, st, p, now)
	if err != nil {
		return err
	}
	keys := make([]string, len(cands))
	for i, c := range cands {
		keys[i] = c.Key
	}
	claimed, err := st.ClaimPush(ctx, id, keys, now)
	if err != nil {
		return err
	}
	s.log.Info("push subscription created", "seeded", len(claimed))
	return nil
}

// deletePushSubscription answers DELETE /api/v1/push/subscription: it
// deletes the caller's own subscription of the endpoint.
func (s *Server) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	if reason := s.pushOff(); reason != "" {
		writeError(w, http.StatusConflict, reason)
		return
	}
	var req PushUnsubscribeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&req); err != nil || req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	deleted, err := s.pushStoreOf().DeletePushSubscription(r.Context(), req.Endpoint, principalOf(r).Username)
	switch {
	case err != nil:
		s.internalError(w, err)
	case !deleted:
		notFound(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
