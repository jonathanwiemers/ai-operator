// Package webpush sends Web Push messages with the standard library only.
//
// It encrypts a payload for one subscription with the aes128gcm content
// coding (RFC 8188) as Web Push message encryption defines it (RFC 8291),
// signs a VAPID token for the push service (RFC 8292) and POSTs the message
// (RFC 8030). It does not log and does not judge the push service's answer:
// the caller decides what a 404 or 410 means for its subscription.
package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Subscription is a browser's push subscription. P256dh and Auth are
// base64url, as PushSubscription.toJSON gives them, with or without padding.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// decodeKey decodes a base64url key with or without padding.
func decodeKey(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// Key is the application server's VAPID key pair on P-256. The zero Key is
// not usable: get one from GenerateKey or ParseKey.
type Key struct {
	priv *ecdsa.PrivateKey
}

// GenerateKey creates a new VAPID key pair.
func GenerateKey() (Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Key{}, fmt.Errorf("webpush: generate key: %w", err)
	}
	return Key{priv: priv}, nil
}

// ParseKey reads a key pair stored as Public and Private returned it: the
// 65-byte uncompressed public point and the 32-byte private scalar. The
// public key must belong to the private one.
func ParseKey(public, private []byte) (Key, error) {
	if len(public) != 65 || len(private) != 32 {
		return Key{}, errors.New("webpush: parse key: want a 65-byte public and a 32-byte private key")
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), private)
	if err != nil {
		return Key{}, errors.New("webpush: parse key: invalid private key")
	}
	k := Key{priv: priv}
	if subtle.ConstantTimeCompare(k.Public(), public) != 1 {
		return Key{}, errors.New("webpush: parse key: the public key does not match the private key")
	}
	return k, nil
}

// Public returns the 65-byte uncompressed public point.
func (k Key) Public() []byte {
	b, err := k.priv.PublicKey.Bytes()
	if err != nil {
		// Unreachable: the key is always a valid P-256 key.
		panic("webpush: public key: " + err.Error())
	}
	return b
}

// Private returns the 32-byte private scalar.
func (k Key) Private() []byte {
	b, err := k.priv.Bytes()
	if err != nil {
		// Unreachable: the key is always a valid P-256 key.
		panic("webpush: private key: " + err.Error())
	}
	return b
}

// PublicBase64 returns the public key as base64url without padding: the
// applicationServerKey for pushManager.subscribe and the k of VAPID.
func (k Key) PublicBase64() string {
	return base64.RawURLEncoding.EncodeToString(k.Public())
}
