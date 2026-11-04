package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed database-grants.sql
var databaseGrants string

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return applyMigrations(ctx, pool, false)
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool, production bool) error {
	tx, e := pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727101)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); e != nil {
		return e
	}
	entries, e := migrations.ReadDir("migrations")
	if e != nil {
		return e
	}
	for _, f := range entries {
		b, e := migrations.ReadFile("migrations/" + f.Name())
		if e != nil {
			return e
		}
		h := sha256.Sum256(b)
		checksum := hex.EncodeToString(h[:])
		var prior string
		e = tx.QueryRow(ctx, `SELECT checksum FROM baobei_migrations WHERE name=$1`, f.Name()).Scan(&prior)
		if e == nil {
			if prior != checksum {
				return fmt.Errorf("migration checksum mismatch")
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(b)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, f.Name(), checksum); e != nil {
			return e
		}
	}
	if production {
		if e = verifySchema(ctx, tx); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, databaseGrants); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func verifySchema(ctx context.Context, db querier) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	rows, err := db.Query(ctx, `SELECT name, checksum FROM baobei_migrations`)
	if err != nil {
		return err
	}
	defer rows.Close()
	applied := make(map[string]string, len(entries))
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return err
		}
		applied[name] = checksum
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(applied) != len(entries) {
		return errors.New("database migration inventory mismatch")
	}
	for _, entry := range entries {
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		if applied[entry.Name()] != hex.EncodeToString(sum[:]) {
			return errors.New("database migration checksum mismatch")
		}
	}
	return nil
}

func prepareDatabase(ctx context.Context, pool *pgxpool.Pool, environment, command string) error {
	if environment == "development" {
		return migrate(ctx, pool)
	}
	role := "baobei_runtime"
	switch command {
	case "migrate":
		role = "baobei_migrator"
	case "identity-apply":
		role = "baobei_maintenance"
	}
	var allowed bool
	err := pool.QueryRow(ctx, `
        SELECT current_user = $1 AND session_user = $1
            AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
            AND NOT EXISTS (SELECT 1 FROM pg_auth_members WHERE member = pg_roles.oid)
            AND NOT has_database_privilege(current_database(), 'CREATE')
            AND NOT has_database_privilege(current_database(), 'TEMP')
            AND has_schema_privilege('public', 'CREATE') = $2
        FROM pg_roles WHERE rolname = current_user
    `, role, command == "migrate").Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("production database role rejected")
	}
	if command == "migrate" {
		return applyMigrations(ctx, pool, true)
	}
	return verifySchema(ctx, pool)
}
