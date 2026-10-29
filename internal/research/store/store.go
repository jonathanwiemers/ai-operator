// Package store keeps research runs, their results and items in Postgres.
// Nothing opens a connection unless a database host is configured.
package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is a connection built from the parts Zalando's postgres-operator
// provides: a username/password Secret per login, the rest configuration.
type Config struct {
	Host     string
	Port     int
	Database string
	// SSLMode is disable, require (the default), verify-ca or verify-full.
	// verify-ca and verify-full check the server against the system roots.
	SSLMode  string
	User     string
	Password string
}

// PoolConfig sets the connection's fields directly, without a URI, so
// passwords may contain any character. PG* environment variables do not
// change the target or the TLS mode.
func (c Config) PoolConfig() (*pgxpool.Config, error) {
	if c.Host == "" || c.Database == "" || c.User == "" {
		return nil, errors.New("research database: host, database and user are required")
	}
	port := c.Port
	if port == 0 {
		port = 5432
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("research database: invalid port %d", c.Port)
	}
	tlsConfig, err := tlsFor(c.SSLMode, c.Host)
	if err != nil {
		return nil, err
	}
	pc, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cc := &pc.ConnConfig.Config
	cc.Host, cc.Port, cc.Database = c.Host, uint16(port), c.Database
	cc.User, cc.Password = c.User, c.Password
	cc.TLSConfig, cc.Fallbacks, cc.ValidateConnect = tlsConfig, nil, nil
	cc.ConnectTimeout = 10 * time.Second
	cc.RuntimeParams = map[string]string{"application_name": "ai-operator"}
	return pc, nil
}

// tlsFor follows libpq's meaning of the SSL modes.
func tlsFor(mode, host string) (*tls.Config, error) {
	switch mode {
	case "disable":
		return nil, nil
	case "", "require":
		// Encrypted, not verified: Zalando's default certificates are
		// self-signed.
		return &tls.Config{InsecureSkipVerify: true}, nil
	case "verify-ca":
		// verifyChain checks the chain; only the host name is skipped.
		return &tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: verifyChain}, nil
	case "verify-full":
		return &tls.Config{ServerName: host}, nil
	}
	return nil, fmt.Errorf("research database: invalid sslMode %q (disable, require, verify-ca or verify-full)", mode)
}

// verifyChain checks the server's certificate chain against the system roots
// and ignores the host name, like libpq's verify-ca.
func verifyChain(raw [][]byte, _ [][]*x509.Certificate) error {
	if len(raw) == 0 {
		return errors.New("server sent no certificate")
	}
	certs := make([]*x509.Certificate, len(raw))
	for i, der := range raw {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return err
		}
		certs[i] = c
	}
	opts := x509.VerifyOptions{Intermediates: x509.NewCertPool()}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(opts)
	return err
}

// Store is a connection pool to the research database.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects with c and pings the database.
func Open(ctx context.Context, c Config) (*Store, error) {
	pc, err := c.PoolConfig()
	if err != nil {
		return nil, err
	}
	return OpenPool(ctx, pc)
}

// OpenPool connects with a prepared pool configuration (tests set the
// search_path) and pings the database.
func OpenPool(ctx context.Context, pc *pgxpool.Config) (*Store, error) {
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("research database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("research database: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close closes the pool.
func (s *Store) Close() { s.pool.Close() }
