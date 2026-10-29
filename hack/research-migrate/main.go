/*
Copyright 2026 Jonathan Wiemers.

Licensed under the Apache License, Version 2.0.
*/
// Command research-migrate prepares a local research database for UI
// development (make research-seed): on the Postgres of make test-db it
// creates the logins research_owner and research_reader and the database
// ai_operator_dev owned by the owner, migrates it as the owner like the
// operator does (granting the reader), and applies the seed SQL as the
// owner. The passwords are fixed: for a loopback database only.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// The logins and database it creates.
const (
	ownerRole      = "research_owner"
	ownerPassword  = "owner"
	readerRole     = "research_reader"
	readerPassword = "reader"
	database       = "ai_operator_dev"
)

func main() {
	admin := flag.String("admin-url", "postgres://postgres:postgres@127.0.0.1:55432/ai_operator_test?sslmode=disable",
		"a superuser's connection URI (the default is make test-db's)")
	seed := flag.String("seed", "hack/research-seed.sql", "SQL applied as the owner after migrating; empty for none")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := run(ctx, *admin, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "research-migrate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, adminURL, seed string) error {
	cc, err := pgx.ParseConfig(adminURL)
	if err != nil {
		return err
	}
	if err := prepare(ctx, cc); err != nil {
		return err
	}
	owner := store.Config{Host: cc.Host, Port: int(cc.Port), Database: database, SSLMode: "disable",
		User: ownerRole, Password: ownerPassword}
	st, err := store.Open(ctx, owner)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx, store.Grants{Reader: readerRole}); err != nil {
		return err
	}
	if seed != "" {
		sql, err := os.ReadFile(seed)
		if err != nil {
			return err
		}
		pc, err := owner.PoolConfig()
		if err != nil {
			return err
		}
		conn, err := pgx.ConnectConfig(ctx, pc.ConnConfig)
		if err != nil {
			return err
		}
		defer conn.Close(context.WithoutCancel(ctx))
		// Without arguments pgx sends the whole file in one simple query.
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", seed, err)
		}
	}
	fmt.Printf("research database %s on %s:%d migrated and seeded; for make run-ui:\n", database, cc.Host, cc.Port)
	fmt.Printf("  export AI_OPERATOR_UI_RESEARCH_DB_USER=%s AI_OPERATOR_UI_RESEARCH_DB_PASSWORD=%s\n", readerRole, readerPassword)
	return nil
}

// prepare creates the logins and the database as the superuser, unless they
// exist.
func prepare(ctx context.Context, cc *pgx.ConnConfig) error {
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	for role, password := range map[string]string{ownerRole: ownerPassword, readerRole: readerPassword} {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			// The password is a constant: no quoting needed.
			if _, err := conn.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD '"+password+"'"); err != nil {
				return err
			}
		}
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_database WHERE datname = $1)", database).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()+
			" OWNER "+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}
