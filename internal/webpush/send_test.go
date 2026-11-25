package webpush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// verifyAuthorization checks a VAPID header against key and returns its
// claims.
func verifyAuthorization(t *testing.T, header string, key Key) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid t=")
	if !ok {
		t.Fatalf("header %q has no vapid t=", header)
	}
	jwt, k, ok := strings.Cut(rest, ", k=")
	if !ok {
		t.Fatalf("header %q has no k=", header)
	}
	if k != key.PublicBase64() {
		t.Errorf("k = %q, want %q", k, key.PublicBase64())
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	var head map[string]string
	if err := json.Unmarshal(b64(t, parts[0]), &head); err != nil || head["typ"] != "JWT" || head["alg"] != "ES256" {
		t.Errorf("header %v (%v)", head, err)
	}
	sig := b64(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature has %d bytes, want raw r||s of 64", len(sig))
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), key.Public())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("signature does not verify")
	}
	var claims map[string]any
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestAuthorization(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	header, err := Authorization("https://fcm.googleapis.com:8443/fcm/send/abc?x=1", key, "https://desk.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	claims := verifyAuthorization(t, header, key)
	if claims["aud"] != "https://fcm.googleapis.com:8443" {
		t.Errorf("aud = %v", claims["aud"])
	}
	if claims["exp"] != float64(now.Add(12*time.Hour).Unix()) {
		t.Errorf("exp = %v, want now + 12 h", claims["exp"])
	}
	if claims["sub"] != "https://desk.example.com" {
		t.Errorf("sub = %v", claims["sub"])
	}

	for _, endpoint := range []string{"/relative/secret", "https://%zz/secret", ""} {
		_, err := Authorization(endpoint, key, "https://desk.example.com", now)
		if err == nil {
			t.Errorf("%q: no error", endpoint)
		} else if endpoint != "" && strings.Contains(err.Error(), "secret") {
			t.Errorf("error %q contains the endpoint", err)
		}
	}
}

func TestSend(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ua, sub := newUserAgent(t)
	payload := []byte(`{"item":{"key":"problem/1"}}`)
	for _, status := range []int{http.StatusCreated, http.StatusGone} {
		var req *http.Request
		var body []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, body = r, must(io.ReadAll(r.Body))
			w.WriteHeader(status)
			_, _ = w.Write([]byte("ignored"))
		}))
		sub.Endpoint = srv.URL + "/push/abc"
		got, err := Send(context.Background(), srv.Client(), sub, key, "https://desk.example.com",
			payload, Message{TTL: 24 * time.Hour, Urgency: "high"})
		srv.Close()
		if err != nil || got != status {
			t.Fatalf("Send = %d, %v; want %d", got, err, status)
		}
		if req.Method != http.MethodPost || req.URL.Path != "/push/abc" {
			t.Errorf("request %s %s", req.Method, req.URL.Path)
		}
		for name, want := range map[string]string{
			"Content-Encoding": "aes128gcm",
			"Content-Type":     "application/octet-stream",
			"TTL":              "86400",
			"Urgency":          "high",
		} {
			if v := req.Header.Get(name); v != want {
				t.Errorf("%s = %q, want %q", name, v, want)
			}
		}
		claims := verifyAuthorization(t, req.Header.Get("Authorization"), key)
		if claims["aud"] != srv.URL {
			t.Errorf("aud = %v, want %s", claims["aud"], srv.URL)
		}
		if got := ua.decrypt(t, body); !bytes.Equal(got, payload) {
			t.Errorf("pushed %q, want %q", got, payload)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestSendNoUrgency(t *testing.T) {
	key, _ := GenerateKey()
	_, sub := newUserAgent(t)
	var urgency []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urgency = r.Header.Values("Urgency")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub.Endpoint = srv.URL + "/push/abc"
	if _, err := Send(context.Background(), srv.Client(), sub, key, "", []byte("x"), Message{}); err != nil {
		t.Fatal(err)
	}
	if urgency != nil {
		t.Errorf("Urgency = %q, want none", urgency)
	}
}

func TestSendErrors(t *testing.T) {
	key, _ := GenerateKey()
	_, sub := newUserAgent(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // nothing listens on its address any more
	sub.Endpoint = srv.URL + "/push/secret-capability"

	_, err := Send(context.Background(), nil, sub, key, "", []byte("x"), Message{})
	if err == nil {
		t.Fatal("unreachable endpoint: no error")
	}
	if msg := err.Error(); strings.Contains(msg, "secret-capability") || strings.Contains(msg, strings.TrimPrefix(srv.URL, "http://")) {
		t.Errorf("error %q contains the endpoint", msg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Send(ctx, nil, sub, key, "", []byte("x"), Message{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}

	bad := sub
	bad.Auth = "AAAA"
	if _, err := Send(context.Background(), nil, bad, key, "", []byte("x"), Message{}); err == nil {
		t.Error("bad subscription: no error")
	}
	bad = sub
	bad.Endpoint = "/push/secret-capability"
	if _, err := Send(context.Background(), nil, bad, key, "", []byte("x"), Message{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("relative endpoint: %v", err)
	}
}

func TestSendError(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.OpError{Op: "dial", Err: timeoutError{}}, "webpush: send: timeout"},
		{&net.DNSError{Name: "push.example.net", Err: "no such host"}, "webpush: send: host not found"},
		{errors.New("tls: bad certificate for https://push.example.net/secret"), "webpush: send: request failed"},
	} {
		if got := sendError(ctx, tc.err).Error(); got != tc.want {
			t.Errorf("sendError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
