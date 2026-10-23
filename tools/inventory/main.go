// Development inventory CLI. Never prints customer fields or database credentials.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"time"
)

func execute(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: inventory validate|import <file> | reset-legacy-simulator-fleet --confirm-legacy-20")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if args[0] == "reset-legacy-simulator-fleet" {
		if len(args) != 2 || args[1] != "--confirm-legacy-20" || os.Getenv("APP_ENV") != "development" {
			return fmt.Errorf("reset requires development and explicit legacy confirmation")
		}
		db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
		if err != nil {
			return err
		}
		defer db.Close()
		return inventory.ResetLegacy(ctx, db, os.Getenv("APP_ENV"))
	}
	if len(args) != 2 || args[0] != "validate" && args[0] != "import" {
		return fmt.Errorf("invalid inventory command")
	}
	d, err := inventory.Load(args[1])
	if err != nil {
		return err
	}
	if args[0] == "validate" {
		fmt.Printf("Inventory valid: projects=%d assets=%d runtimes=%d\n", len(d.Projects), len(d.AssetDevices), len(d.RuntimeDevices))
		return nil
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	return inventory.Import(ctx, db, d)
}
func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Inventory operation failed; validate input and local environment. Customer fields are not logged.")
		os.Exit(1)
	}
}
