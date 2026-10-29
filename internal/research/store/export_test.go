package store

import (
	"context"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations are the embedded migrations, for tests that add their own.
var Migrations fs.FS = migrations

// Pool lets tests run SQL as the store's login.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// MigrateFS migrates with the migrations/*.sql of fsys instead of the
// embedded ones.
func (s *Store) MigrateFS(ctx context.Context, fsys fs.FS, g Grants) error {
	return s.migrate(ctx, fsys, g)
}
