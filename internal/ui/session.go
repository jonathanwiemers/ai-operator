package ui

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Sessions are kept in encrypted cookies (AES-GCM), so the UI holds no state
// and several replicas share logins through the session key. Cookies carry
// the user name and roles, never the provider's tokens.
type Sessions struct {
	aead   cipher.AEAD
	secure bool
	ttl    time.Duration
	now    func() time.Time
}

// Session is the content of the session cookie.
type Session struct {
	Principal
	Expires int64 `json:"exp"`
}

// LoginState is the content of the cookie that carries a login from the
// redirect to the provider to the callback.
type LoginState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Return   string `json:"return"`
	Expires  int64  `json:"exp"`
}

const loginStateTTL = 10 * time.Minute

// maxCookieSize stays below the 4096 bytes browsers keep per cookie.
const maxCookieSize = 3800

// NewSessions derives the cookie key from the session key.
func NewSessions(key string, secure bool, ttl time.Duration) (*Sessions, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sessions{aead: aead, secure: secure, ttl: ttl, now: time.Now}, nil
}

// SessionCookie and LoginCookie are the cookie names. With https they get
// the __Host- prefix: the browser then only accepts them from this host,
// over https, for the whole site.
func (s *Sessions) SessionCookie() string { return s.cookieName("ai-operator-session") }
func (s *Sessions) LoginCookie() string   { return s.cookieName("ai-operator-login") }

func (s *Sessions) cookieName(base string) string {
	if s.secure {
		return "__Host-" + base
	}
	return base
}

// Start writes the session cookie for p.
func (s *Sessions) Start(w http.ResponseWriter, p Principal) error {
	exp := s.now().Add(s.ttl)
	return s.write(w, s.SessionCookie(), Session{Principal: p, Expires: exp.Unix()}, exp)
}

// Get returns the session of the request, if it has a valid one.
func (s *Sessions) Get(r *http.Request) (*Session, bool) {
	var sess Session
	if !s.read(r, s.SessionCookie(), &sess) || s.now().Unix() >= sess.Expires {
		return nil, false
	}
	return &sess, true
}

// StartLogin writes the login cookie.
func (s *Sessions) StartLogin(w http.ResponseWriter, st LoginState) error {
	exp := s.now().Add(loginStateTTL)
	st.Expires = exp.Unix()
	return s.write(w, s.LoginCookie(), st, exp)
}

// Login returns the login state of the request and clears its cookie.
func (s *Sessions) Login(w http.ResponseWriter, r *http.Request) (*LoginState, bool) {
	var st LoginState
	ok := s.read(r, s.LoginCookie(), &st) && s.now().Unix() < st.Expires
	s.Clear(w, s.LoginCookie())
	return &st, ok
}

// Clear deletes a cookie.
func (s *Sessions) Clear(w http.ResponseWriter, name string) {
	c := s.cookie(name, "", time.Unix(0, 0))
	c.MaxAge = -1
	http.SetCookie(w, c)
}

func (s *Sessions) write(w http.ResponseWriter, name string, v any, exp time.Time) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	// The cookie name is the additional data, so one cookie's value is
	// never accepted as another's.
	sealed := s.aead.Seal(nonce, nonce, plain, []byte(name))
	value := base64.RawURLEncoding.EncodeToString(sealed)
	if len(value) > maxCookieSize {
		return errors.New("the session does not fit into a cookie; return fewer roles from oidc.claims.roles")
	}
	c := s.cookie(name, value, exp)
	c.MaxAge = int(exp.Sub(s.now()).Seconds())
	http.SetCookie(w, c)
	return nil
}

func (s *Sessions) read(r *http.Request, name string, v any) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return false
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(name))
	if err != nil {
		return false
	}
	return json.Unmarshal(plain, v) == nil
}

func (s *Sessions) cookie(name, value string, exp time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   s.secure,
		// Lax: sent along when the provider redirects back to the callback,
		// not with requests other sites make.
		SameSite: http.SameSiteLaxMode,
	}
}
