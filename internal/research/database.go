package research

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// DatabaseConfig is where the research database is, without a login: the
// operator connects as the owner, research pods with their project's writer.
type DatabaseConfig struct {
	Host    string
	Port    int
	Name    string
	SSLMode string
}

// Login returns the connection of user to the database. It fails on
// incomplete or invalid configuration.
func (c DatabaseConfig) Login(user, password string) (store.Config, error) {
	sc := store.Config{Host: c.Host, Port: c.Port, Database: c.Name, SSLMode: c.SSLMode, User: user, Password: password}
	if c.Name == "" || user == "" || password == "" {
		return sc, errors.New("research database: name, user and password are required with a host")
	}
	_, err := sc.PoolConfig()
	return sc, err
}

const (
	retention    = time.Hour
	minRetry     = 5 * time.Second
	maxRetry     = 5 * time.Minute
	setupTimeout = 2 * time.Minute
)

// Database connects to the research database as the owner, applies the
// migrations and grants, and then deletes expired runs hourly. It runs on
// the leader only and never fails the manager: it retries until the
// database is ready, so the factory keeps working during an outage.
type Database struct {
	grants store.Grants
	clock  clock.WithTicker
	log    logr.Logger
	open   func(context.Context) (*store.Store, error)
	ready  chan struct{}
	store  *store.Store
}

// NewDatabase returns the runnable for the owner login c.
func NewDatabase(c store.Config, g store.Grants, clk clock.WithTicker, log logr.Logger) *Database {
	return &Database{grants: g, clock: clk, log: log, ready: make(chan struct{}),
		open: func(ctx context.Context) (*store.Store, error) { return store.Open(ctx, c) }}
}

// NeedLeaderElection makes it a leader-only runnable: one replica migrates.
func (d *Database) NeedLeaderElection() bool { return true }

// Ready is closed once the store is migrated and granted.
func (d *Database) Ready() <-chan struct{} { return d.ready }

// Store returns the owner's store, or nil before Ready.
func (d *Database) Store() *store.Store {
	select {
	case <-d.ready:
		return d.store
	default:
		return nil
	}
}

// Start sets up the database and runs the retention loop until ctx ends.
func (d *Database) Start(ctx context.Context) error {
	for delay := minRetry; ; delay = min(2*delay, maxRetry) {
		s, err := d.setup(ctx)
		if err == nil {
			d.store = s
			break
		}
		d.log.Error(err, "research database not ready, retrying", "in", delay)
		select {
		case <-ctx.Done():
			return nil
		case <-d.clock.After(delay):
		}
	}
	defer d.store.Close()
	close(d.ready)
	d.log.Info("research database ready")
	d.deleteExpired(ctx)
	tick := d.clock.NewTicker(retention)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C():
			d.deleteExpired(ctx)
		}
	}
}

func (d *Database) setup(ctx context.Context) (*store.Store, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	s, err := d.open(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Migrate(ctx, d.grants); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (d *Database) deleteExpired(ctx context.Context) {
	n, err := d.store.DeleteExpired(ctx, d.clock.Now())
	if err != nil {
		d.log.Error(err, "deleting expired research runs")
		return
	}
	d.log.Info("deleted expired research runs", "runs", n)
}

// FinishRunOrInsert records a run's outcome in the store, see
// store.Store.FinishRunOrInsert. It fails before Ready.
func (d *Database) FinishRunOrInsert(ctx context.Context, r store.Run) error {
	s := d.Store()
	if s == nil {
		return errors.New("research database is not ready")
	}
	return s.FinishRunOrInsert(ctx, r)
}
