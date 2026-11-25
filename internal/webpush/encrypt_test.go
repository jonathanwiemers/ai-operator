package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

// TestEncryptRFC8291 reproduces the example of RFC 8291 Section 5 with the
// inputs of its Appendix A.
func TestEncryptRFC8291(t *testing.T) {
	asPrivate, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{
		Endpoint: "https://push.example.net/push/JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV",
		P256dh:   "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		// Padded, as some clients send it.
		Auth: "BTBZMqHH6r4Tts7J_aSIgg==",
	}
	got, err := encrypt(sub, []byte("When I grow up, I want to be a watermelon"), asPrivate, b64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml"+
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT"+
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")
	if !bytes.Equal(got, want) {
		t.Errorf("body:\n got %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), base64.RawURLEncoding.EncodeToString(want))
	}
}

// userAgent is the browser side of a subscription.
type userAgent struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newUserAgent(t *testing.T) (userAgent, Subscription) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ua := userAgent{priv: priv, auth: make([]byte, 16)}
	_, _ = rand.Read(ua.auth)
	return ua, Subscription{
		Endpoint: "https://push.example.net/push/abc",
		P256dh:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(ua.auth),
	}
}

// decrypt is the receiving side of RFC 8291 and RFC 8188, written apart from
// encrypt.
func (ua userAgent) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != 4096 || idlen != 65 {
		t.Fatalf("header: rs %d, idlen %d", rs, idlen)
	}
	asPublicRaw, record := body[21:21+idlen], body[21+idlen:]
	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.priv.ECDH(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), ua.priv.PublicKey().Bytes()...), asPublicRaw...)
	ikm, _ := hkdf.Key(sha256.New, secret, ua.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("no last-record delimiter: %x", plain)
	}
	return plain[:len(plain)-1]
}

func TestEncryptRoundTrip(t *testing.T) {
	ua, sub := newUserAgent(t)
	payload := []byte(`{"item":{"key":"problem/1"}}`)
	a, err := Encrypt(sub, payload)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt(sub, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := ua.decrypt(t, a); !bytes.Equal(got, payload) {
		t.Errorf("decrypted %q, want %q", got, payload)
	}
	if bytes.Equal(a[:16], b[:16]) || bytes.Equal(a[21:86], b[21:86]) {
		t.Error("two messages share a salt or an ephemeral key")
	}
	if _, err := Encrypt(sub, make([]byte, MaxPayload)); err != nil {
		t.Errorf("payload of MaxPayload bytes: %v", err)
	}
	if body, _ := Encrypt(sub, make([]byte, MaxPayload)); len(body) != 4096 {
		t.Errorf("body of the largest payload has %d bytes, want 4096", len(body))
	}
}

func TestEncryptRejects(t *testing.T) {
	_, sub := newUserAgent(t)
	for name, tc := range map[string]struct {
		mod     func(*Subscription)
		payload int
		want    string
	}{
		"payload too large": {func(*Subscription) {}, MaxPayload + 1, "at most"},
		"p256dh not base64": {func(s *Subscription) { s.P256dh = "!!" }, 1, "p256dh"},
		"p256dh not a key":  {func(s *Subscription) { s.P256dh = "AAAA" }, 1, "p256dh"},
		"auth too short":    {func(s *Subscription) { s.Auth = "AAAA" }, 1, "auth"},
		"auth not base64":   {func(s *Subscription) { s.Auth = "!!" }, 1, "auth"},
	} {
		t.Run(name, func(t *testing.T) {
			s := sub
			tc.mod(&s)
			_, err := Encrypt(s, make([]byte, tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one with %q", err, tc.want)
			}
		})
	}
}

func TestParseKey(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Public()) != 65 || len(k.Private()) != 32 {
		t.Fatalf("lengths: public %d, private %d", len(k.Public()), len(k.Private()))
	}
	parsed, err := ParseKey(k.Public(), k.Private())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed.Public(), k.Public()) || !bytes.Equal(parsed.Private(), k.Private()) {
		t.Error("parsed key differs")
	}
	if got := k.PublicBase64(); got != base64.RawURLEncoding.EncodeToString(k.Public()) || strings.Contains(got, "=") {
		t.Errorf("PublicBase64 = %q", got)
	}

	other, _ := GenerateKey()
	for name, tc := range map[string]struct{ pub, priv []byte }{
		"short public":     {k.Public()[:64], k.Private()},
		"short private":    {k.Public(), k.Private()[:31]},
		"long private":     {k.Public(), append(k.Private(), 0)},
		"zero private":     {k.Public(), make([]byte, 32)},
		"other public key": {other.Public(), k.Private()},
	} {
		if _, err := ParseKey(tc.pub, tc.priv); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
