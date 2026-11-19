// Package testsupport contains isolated integration fixtures, never production initialization.
package testsupport

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// urls remembers the connection string of every isolated database (for code under test that opens
// its own pool, e.g. host tools).
var (
	urlsMu sync.Mutex
	urls   = map[*sql.DB]string{}
)

// URL returns the connection string of an isolated database created by Database.
func URL(t *testing.T, db *sql.DB) string {
	t.Helper()
	urlsMu.Lock()
	u, ok := urls[db]
	urlsMu.Unlock()
	if !ok {
		t.Fatal("not an isolated test database")
	}
	return u
}

func Database(t *testing.T) *sql.DB {
	t.Helper()
	source := os.Getenv("A1_TEST_DATABASE_URL")
	if source == "" {
		t.Skip("A1 real PostgreSQL tests run in the isolated integration fixture")
	}
	root, err := sql.Open("pgx", source)
	if err != nil {
		t.Fatal(err)
	}
	name := "crane_a1_test_" + platform.ID()
	if !contract.ValidID(name) {
		t.Fatal("invalid test DB identifier")
	}
	if _, err = root.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal("isolated test DB create failed")
	}
	u, err := url.Parse(source)
	if err != nil {
		t.Fatal("invalid fixture connection")
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	urlsMu.Lock()
	urls[db] = u.String()
	urlsMu.Unlock()
	t.Cleanup(func() {
		db.Close()
		_, err := root.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		root.Close()
		if err != nil {
			t.Error("isolated test DB cleanup failed")
		}
	})
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrations.Files)
	if err = goose.SetDialect("postgres"); err == nil {
		err = goose.UpContext(context.Background(), db, ".")
	}
	if err != nil {
		t.Fatal("ordered test migrations failed", err)
	}
	return db
}
