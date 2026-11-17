// equipment-types is an explicit host-administrator tool that initializes and
// checks the equipment type directory (R6-BIZ-R1 F-02). It is not an HTTP endpoint
// and no client can submit a type code through it: every code comes from the
// code-owned registry.
//
//	equipment-types plan   (--all | --types MOTOR,PLC)
//	equipment-types apply  (--all | --types …) --approve <plan digest> --actor <admin id>
//	equipment-types verify (--all | --types …) [--expect-installable N]
//
// plan and verify are read-only. apply writes only the missing registry types, in
// one transaction, and refuses unless --approve equals the digest of the current
// plan (a state change since the approval refuses the apply). Output is JSON and
// never contains the database URL or password.
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

	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Exit codes: 0 ok; 1 refused / failed; 3 verify found drift.
const exitDrift = 3

type options struct {
	op      string
	all     bool
	types   []string
	approve string
	actor   string
	expect  int
}

func parse(args []string) (options, error) {
	if len(args) == 0 {
		return options{}, errors.New("usage: equipment-types plan|apply|verify (--all | --types A,B) [--approve DIGEST --actor ID]")
	}
	o := options{op: args[0]}
	if o.op != "plan" && o.op != "apply" && o.op != "verify" {
		return options{}, errors.New("unknown operation")
	}
	flags := flag.NewFlagSet("equipment-types", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	all := flags.Bool("all", false, "every type of the code registry")
	types := flags.String("types", "", "comma-separated registered type codes")
	approve := flags.String("approve", "", "digest of the approved plan (apply only)")
	actor := flags.String("actor", "", "explicit host administrator identity (apply only)")
	expect := flags.Int("expect-installable", -1, "verify only: required number of installable types in scope")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return options{}, errors.New("invalid arguments")
	}
	o.all, o.approve, o.actor, o.expect = *all, strings.TrimSpace(*approve), strings.TrimSpace(*actor), *expect
	if o.expect != -1 && (o.op != "verify" || o.expect < 0) {
		return options{}, errors.New("--expect-installable is only accepted by verify (N >= 0)")
	}
	if *types != "" {
		o.types = strings.Split(*types, ",")
	}
	if o.op == "apply" && (o.approve == "" || o.actor == "") {
		return options{}, errors.New("apply requires --approve <plan digest> and --actor <administrator>")
	}
	if o.op != "apply" && (o.approve != "" || o.actor != "") {
		return options{}, errors.New("--approve/--actor are only accepted by apply")
	}
	return o, nil
}

// openDB follows tools/identity: the URL is password-free and the password comes
// only from a private file outside development. Neither is ever printed.
func openDB() (*sql.DB, error) {
	source := os.Getenv("DATABASE_URL")
	if path := os.Getenv("DATABASE_PASSWORD_FILE"); path != "" {
		password, err := platform.PrivateSecret(path, 20)
		if err != nil {
			return nil, err
		}
		source, err = platform.DatabaseWithPassword(source, password)
		if err != nil {
			return nil, err
		}
	} else if os.Getenv("APP_ENV") != "development" {
		return nil, errors.New("equipment-types requires a DB password file outside development")
	}
	if source == "" {
		return nil, errors.New("DATABASE_URL required")
	}
	return sql.Open("pgx", source)
}

func execute(args []string, out io.Writer) (int, error) {
	o, err := parse(args)
	if err != nil {
		return 1, err
	}
	scope, err := equipmenttypes.Scope(o.all, o.types)
	if err != nil {
		return 1, err
	}
	db, err := openDB()
	if err != nil {
		return 1, errors.New("equipment-types database unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	switch o.op {
	case "plan":
		p, err := equipmenttypes.PlanTypes(ctx, db, scope)
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "plan", "plan": p})
	case "apply":
		rec, err := equipmenttypes.Apply(ctx, db, scope, o.approve, o.actor, platform.ID())
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "apply", "record": rec})
	default:
		r, err := equipmenttypes.Verify(ctx, db, scope)
		if err != nil {
			return 1, err
		}
		if err := enc.Encode(map[string]any{"operation": "verify", "report": r}); err != nil {
			return 1, err
		}
		if !r.Consistent || (o.expect >= 0 && len(r.Installable) != o.expect) {
			return exitDrift, nil
		}
		return 0, nil
	}
}

// safeReason maps an error to an operator message that never echoes connection
// details (driver errors can embed host/user names).
func safeReason(err error) string {
	for _, known := range []error{equipmenttypes.ErrUnknownType, equipmenttypes.ErrEmptyScope, equipmenttypes.ErrDigestMismatch, equipmenttypes.ErrConflict, equipmenttypes.ErrActorRequired} {
		if errors.Is(err, known) {
			return err.Error()
		}
	}
	switch msg := err.Error(); {
	case strings.HasPrefix(msg, "usage:"), msg == "unknown operation", msg == "invalid arguments",
		strings.HasPrefix(msg, "apply requires"), strings.HasPrefix(msg, "--approve/--actor"),
		strings.HasPrefix(msg, "choose either"), strings.HasPrefix(msg, "--expect-installable"), msg == "equipment-types database unavailable":
		return msg
	}
	return "operation failed; check private DB configuration and retry (details withheld)"
}

func main() {
	code, err := execute(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Equipment type directory operation rejected: "+safeReason(err))
		os.Exit(1)
	}
	os.Exit(code)
}
