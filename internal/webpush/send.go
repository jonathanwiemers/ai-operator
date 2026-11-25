package webpush

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// Message holds the delivery options of one push (RFC 8030 §5.2, §5.3).
type Message struct {
	// TTL is how long the push service keeps the message for an offline
	// device; it is sent in whole seconds.
	TTL time.Duration
	// Urgency is "very-low", "low", "normal" or "high"; empty sends none.
	Urgency string
}

// Send encrypts payload for sub and POSTs it to the subscription's endpoint,
// signed with key and subject (see Authorization). It returns the push
// service's status code without judging it: the caller decides what 404 or
// 410 means. A nil client is http.DefaultClient. Errors never contain the
// endpoint, the keys or the payload.
func Send(ctx context.Context, client *http.Client, sub Subscription, key Key, subject string, payload []byte, msg Message) (int, error) {
	body, err := Encrypt(sub, payload)
	if err != nil {
		return 0, err
	}
	auth, err := Authorization(sub.Endpoint, key, subject, time.Now())
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("webpush: send: invalid endpoint")
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.FormatInt(int64(msg.TTL/time.Second), 10))
	if msg.Urgency != "" {
		req.Header.Set("Urgency", msg.Urgency)
	}
	req.Header.Set("Authorization", auth)
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, sendError(ctx, err)
	}
	// Drain a bounded part of the body so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// sendError replaces a transport error, whose *url.Error names the endpoint
// and whose inner errors may, with one that keeps only its kind.
func sendError(ctx context.Context, err error) error {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("webpush: send: %w", ctx.Err())
	case errors.As(err, &netErr) && netErr.Timeout():
		return errors.New("webpush: send: timeout")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("webpush: send: connection refused")
	case errors.As(err, &dnsErr):
		return errors.New("webpush: send: host not found")
	default:
		return errors.New("webpush: send: request failed")
	}
}
