package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
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
		if e.Error() != "no rows in result set" {
			return e
		}
		if _, e = tx.Exec(ctx, string(b)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, f.Name(), checksum); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

type Config struct {
	Env, Addr, Origin, Issuer, ClientID, Secret string
	Dev                                         bool
}

func config() (Config, error) {
	c := Config{os.Getenv("APP_ENV"), os.Getenv("LISTEN_ADDR"), os.Getenv("PUBLIC_ORIGIN"), os.Getenv("OIDC_ISSUER"), os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"), os.Getenv("DEV_AUTH") == "1"}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:8088"
	}
	if c.Origin == "" {
		c.Origin = "http://127.0.0.1:5188"
	}
	if c.Env != "development" && c.Env != "production" {
		return c, fmt.Errorf("explicit APP_ENV required")
	}
	if c.Dev {
		host, _, e := net.SplitHostPort(c.Addr)
		if e != nil || c.Env != "development" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return c, fmt.Errorf("dev auth requires development and loopback listen")
		}
	}
	if c.Env == "production" && (c.Origin != "https://baobei.youdaozd.com" || c.Issuer == "" || !strings.HasPrefix(c.Issuer, "https://") || c.ClientID != "baobei-web" || c.Secret == "") {
		return c, fmt.Errorf("production OIDC configuration required")
	}
	return c, nil
}
func main() {
	c, e := config()
	if e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("explicit DATABASE_URL required")
	}
	pool, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("database configuration failed")
	}
	defer pool.Close()
	if e = migrate(ctx, pool); e != nil {
		log.Fatal("database migration failed")
	}
	a, e := newApp(ctx, pool, c)
	if e != nil {
		log.Fatal("authentication initialization failed")
	}
	if c.Dev {
		if e = seed(ctx, pool); e != nil {
			log.Fatal("synthetic identity seed failed")
		}
	}
	server := &http.Server{Addr: c.Addr, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("baobei API listening (request logging disabled)")
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal("server stopped")
	}
}
func seed(ctx context.Context, p *pgxpool.Pool) error {
	_, e := p.Exec(ctx, `INSERT INTO teams(id,name) VALUES('11111111-1111-4111-8111-111111111111','Synthetic Team') ON CONFLICT DO NOTHING;
 INSERT INTO app_users(subject,display_name,role,team_id) VALUES
 ('synthetic-a','示例销售 A','baobei_sales','11111111-1111-4111-8111-111111111111'),
 ('synthetic-b','示例销售 B','baobei_sales','11111111-1111-4111-8111-111111111111'),
 ('synthetic-manager','示例主管','baobei_manager','11111111-1111-4111-8111-111111111111') ON CONFLICT DO NOTHING`)
	return e
}
