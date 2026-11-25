package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// recordSize is the rs of the aes128gcm header: one record holds the
	// whole message.
	recordSize = 4096
	// headerSize is salt (16), rs (4), idlen (1) and the 65-byte keyid.
	headerSize = 16 + 4 + 1 + 65
	// MaxPayload is the largest payload Encrypt accepts. A push service must
	// accept a body of 4096 bytes (RFC 8291 §4): the header, the padding
	// delimiter and the 16-byte tag leave 3993 bytes for the payload.
	MaxPayload = recordSize - headerSize - 1 - 16
)

// Encrypt encrypts payload for sub as one aes128gcm record (RFC 8188) with a
// fresh ephemeral ECDH key and a random salt (RFC 8291). The result is the
// request body for the push service.
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	asPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	return encrypt(sub, payload, asPrivate, salt)
}

// encrypt is Encrypt with the application server's ephemeral key and the
// salt given, so a test can reproduce RFC 8291 Appendix A.
func encrypt(sub Subscription, payload []byte, asPrivate *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("webpush: encrypt: the payload has %d bytes, at most %d fit one record", len(payload), MaxPayload)
	}
	uaRaw, err := decodeKey(sub.P256dh)
	if err != nil {
		return nil, errors.New("webpush: encrypt: the subscription's p256dh is not base64url")
	}
	uaPublic, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, errors.New("webpush: encrypt: the subscription's p256dh is not a P-256 public key")
	}
	auth, err := decodeKey(sub.Auth)
	if err != nil || len(auth) != 16 {
		return nil, errors.New("webpush: encrypt: the subscription's auth is not a 16-byte base64url secret")
	}
	secret, err := asPrivate.ECDH(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	asPublic := asPrivate.PublicKey().Bytes()

	// RFC 8291 §3.3: combine the ECDH secret with the auth secret.
	keyInfo := "WebPush: info\x00" + string(uaRaw) + string(asPublic)
	ikm, err := hkdf.Key(sha256.New, secret, auth, keyInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	// RFC 8188 §2.2 and §2.3: the content encryption key and the nonce.
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webpush: encrypt: %w", err)
	}

	// RFC 8188 §2.1: salt, rs, idlen and keyid, then the one record: the
	// payload with the last-record padding delimiter 0x02, encrypted.
	body := make([]byte, 0, headerSize+len(payload)+1+gcm.Overhead())
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, recordSize)
	body = append(body, byte(len(asPublic)))
	body = append(body, asPublic...)
	plain := append(append(make([]byte, 0, len(payload)+1), payload...), 0x02)
	return gcm.Seal(body, nonce, plain, nil), nil
}
