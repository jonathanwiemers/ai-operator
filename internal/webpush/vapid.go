package webpush

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// tokenLifetime is how long a VAPID token is valid; RFC 8292 allows at most
// 24 hours.
const tokenLifetime = 12 * time.Hour

// jwtHeader is the fixed JOSE header of a VAPID token, base64url.
var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))

// Authorization returns the Authorization header for a push to endpoint
// (RFC 8292): "vapid t=<jwt>, k=<public key>". The JWT is signed with key
// (ES256), its audience is the endpoint's origin, it expires 12 hours after
// now, and its subject is subject, a contact URL of the application server.
// Errors do not contain the endpoint.
func Authorization(endpoint string, key Key, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("webpush: authorization: the endpoint is not an absolute URL")
	}
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{u.Scheme + "://" + u.Host, now.Add(tokenLifetime).Unix(), subject})
	if err != nil {
		return "", fmt.Errorf("webpush: authorization: %w", err)
	}
	signed := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, key.priv, digest[:])
	if err != nil {
		return "", fmt.Errorf("webpush: authorization: %w", err)
	}
	// JWS ES256 wants the raw r||s, each 32 bytes, not ASN.1 (RFC 7518 §3.4).
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signed + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + key.PublicBase64(), nil
}
