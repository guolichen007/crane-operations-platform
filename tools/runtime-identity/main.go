// runtime-identity is an explicit host-administrator tool that registers SIMULATION / TEST runtime
// identities against existing assets (R7). It never creates projects or assets, never registers
// REAL runtimes and never changes an existing identity.
//
//	runtime-identity plan   --request req.json
//	runtime-identity apply  --request req.json --approve <plan digest> --actor <admin>
//	runtime-identity verify --request req.json
//	runtime-identity fleet  --request req.json        (prints the Fleet manifest; no database)
//	runtime-identity merge  --request req.json --fleet current.json [--simulator]   (merged Fleet; no database)
//	                        --simulator prints ONLY this request's SIMULATION identities (the
//	                        simulator run Fleet); the cloud Fleet is the output without --simulator
//	runtime-identity acl    --fleet fleet.json        (broker ACL for exactly that Fleet; no database)
//
// The database URL is password-free; the password comes only from DATABASE_PASSWORD_FILE.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/runtimeidentity"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const exitDrift = 3

func execute(args []string, out io.Writer) (int, error) {
	if len(args) == 0 {
		return 1, errors.New("usage: runtime-identity plan|apply|verify|fleet --request FILE [--approve DIGEST --actor ID]")
	}
	op := args[0]
	if op == "acl" {
		return aclCommand(args[1:], out)
	}
	if op != "plan" && op != "apply" && op != "verify" && op != "fleet" && op != "merge" {
		return 1, errors.New("unknown operation")
	}
	flags := flag.NewFlagSet("runtime-identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("request", "", "explicit registration request (JSON)")
	approve := flags.String("approve", "", "approved plan digest (apply only)")
	actor := flags.String("actor", "", "host administrator (apply only)")
	current := flags.String("fleet", "", "current Fleet manifest (merge only)")
	simulator := flags.Bool("simulator", false, "merge: output only this request's SIMULATION identities")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" {
		return 1, errors.New("invalid arguments")
	}
	if op == "apply" && (strings.TrimSpace(*approve) == "" || strings.TrimSpace(*actor) == "") {
		return 1, errors.New("apply requires --approve and --actor")
	}
	if op != "apply" && (*approve != "" || *actor != "") {
		return 1, errors.New("--approve/--actor are only accepted by apply")
	}
	var req runtimeidentity.Request
	if err := fleet.Read(*path, &req); err != nil {
		return 1, errors.New("cannot read request")
	}
	if err := runtimeidentity.ValidateRequest(req); err != nil {
		return 1, err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if op == "fleet" {
		m := runtimeidentity.Fleet(req)
		if err := fleet.Validate(m, false); err != nil {
			return 1, err
		}
		return 0, enc.Encode(m)
	}
	if op == "merge" {
		var existing contract.FleetManifest
		if *current != "" {
			if err := fleet.Read(*current, &existing); err != nil {
				return 1, errors.New("cannot read current fleet")
			}
		}
		m, err := runtimeidentity.MergeFleet(existing, req)
		if err != nil {
			return 1, err
		}
		if err := fleet.Validate(m, false); err != nil {
			return 1, err
		}
		if *simulator {
			m = runtimeidentity.RequestSimulatorFleet(m, req)
			if err := fleet.Validate(m, true); err != nil {
				return 1, err
			}
		}
		return 0, enc.Encode(m)
	}
	db, err := openDB()
	if err != nil {
		return 1, errors.New("runtime-identity database unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch op {
	case "plan":
		p, err := runtimeidentity.PlanRegistration(ctx, db, req)
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "plan", "plan": p})
	case "apply":
		rec, err := runtimeidentity.Apply(ctx, db, req, strings.TrimSpace(*approve), strings.TrimSpace(*actor), platform.ID())
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "apply", "record": rec})
	default:
		r, err := runtimeidentity.Verify(ctx, db, req)
		if err != nil {
			return 1, err
		}
		if err := enc.Encode(map[string]any{"operation": "verify", "report": r}); err != nil {
			return 1, err
		}
		if !r.Consistent {
			return exitDrift, nil
		}
		return 0, nil
	}
}

func aclCommand(args []string, out io.Writer) (int, error) {
	flags := flag.NewFlagSet("acl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("fleet", "", "Fleet manifest")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *path == "" {
		return 1, errors.New("invalid arguments")
	}
	var m contract.FleetManifest
	if err := fleet.Read(*path, &m); err != nil {
		return 1, errors.New("cannot read fleet")
	}
	acl, err := fleet.ACL(m)
	if err != nil {
		return 1, err
	}
	_, err = io.WriteString(out, acl)
	return 0, err
}

func openDB() (*sql.DB, error) {
	source := os.Getenv("DATABASE_URL")
	if p := os.Getenv("DATABASE_PASSWORD_FILE"); p != "" {
		password, err := platform.PrivateSecret(p, 20)
		if err != nil {
			return nil, err
		}
		if source, err = platform.DatabaseWithPassword(source, password); err != nil {
			return nil, err
		}
	} else if os.Getenv("APP_ENV") != "development" {
		return nil, errors.New("requires a DB password file outside development")
	}
	if source == "" {
		return nil, errors.New("DATABASE_URL required")
	}
	return sql.Open("pgx", source)
}

func safeReason(err error) string {
	for _, known := range []error{runtimeidentity.ErrRequest, runtimeidentity.ErrDigestMismatch, runtimeidentity.ErrNotApplicable, runtimeidentity.ErrActorRequired} {
		if errors.Is(err, known) {
			return err.Error()
		}
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "usage:") || strings.HasPrefix(msg, "unknown") || strings.HasPrefix(msg, "invalid arguments") || strings.HasPrefix(msg, "apply requires") ||
		strings.HasPrefix(msg, "--approve") || strings.HasPrefix(msg, "cannot read") || strings.Contains(msg, "identity") || strings.Contains(msg, "prefix") || strings.Contains(msg, "database unavailable") || strings.Contains(msg, "runtime identity") || strings.Contains(msg, "fleet must") {
		return msg
	}
	return "operation failed (details withheld)"
}

func main() {
	code, err := execute(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Runtime identity operation rejected: "+safeReason(err))
		os.Exit(1)
	}
	os.Exit(code)
}
