// lhk-sim is the host-administrator tool for the controlled SIMULATION maintenance scenario of the
// 老河口 first asset (R9 收口 G2). Never REAL, never approves a real rule, never ingests telemetry.
//
//	lhk-sim plan
//	lhk-sim apply         --approve <plan digest> --actor <admin>
//	lhk-sim verify        [--fresh-minutes 10]          exit 0 PASS · 3 PENDING · 4 FAIL
//	lhk-sim cleanup-plan  [--include-meter-data]
//	lhk-sim cleanup-apply [--include-meter-data] --approve <cleanup digest> --actor <admin>
//
// Database: DATABASE_URL (no password) + DATABASE_PASSWORD_FILE (private, 0600) as the other host
// tools; output is JSON and never contains the URL or the password.
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

	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/simscenario"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	exitPending = 3
	exitFail    = 4
)

type options struct {
	op, approve, actor string
	meterData          bool
	fresh              int
}

var ops = map[string]bool{"plan": true, "apply": true, "verify": true, "cleanup-plan": true, "cleanup-apply": true}

func parse(args []string) (options, error) {
	if len(args) == 0 || !ops[args[0]] {
		return options{}, errors.New("usage: lhk-sim plan|apply|verify|cleanup-plan|cleanup-apply [--approve DIGEST --actor ID] [--include-meter-data] [--fresh-minutes N]")
	}
	o := options{op: args[0]}
	f := flag.NewFlagSet("lhk-sim", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	approve := f.String("approve", "", "approved digest")
	actor := f.String("actor", "", "explicit host administrator")
	meter := f.Bool("include-meter-data", false, "cleanup also deletes the simulator's SIMULATION meter data")
	fresh := f.Int("fresh-minutes", 10, "verify: newest meter batch must be at most this old")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return o, errors.New("invalid arguments")
	}
	o.approve, o.actor, o.meterData, o.fresh = strings.TrimSpace(*approve), strings.TrimSpace(*actor), *meter, *fresh
	writes := o.op == "apply" || o.op == "cleanup-apply"
	if writes && (o.approve == "" || o.actor == "") {
		return o, errors.New("apply requires --approve <digest> and --actor <administrator>")
	}
	if !writes && (o.approve != "" || o.actor != "") {
		return o, errors.New("--approve/--actor are only accepted by apply / cleanup-apply")
	}
	if o.meterData && !strings.HasPrefix(o.op, "cleanup") {
		return o, errors.New("--include-meter-data is only accepted by cleanup")
	}
	if o.fresh < 1 || o.fresh > 1440 {
		return o, errors.New("invalid arguments")
	}
	return o, nil
}

func openDB() (*sql.DB, error) {
	source := os.Getenv("DATABASE_URL")
	if path := os.Getenv("DATABASE_PASSWORD_FILE"); path != "" {
		password, err := platform.PrivateSecret(path, 20)
		if err != nil {
			return nil, err
		}
		if source, err = platform.DatabaseWithPassword(source, password); err != nil {
			return nil, err
		}
	} else if os.Getenv("APP_ENV") != "development" {
		return nil, errors.New("lhk-sim requires a DB password file outside development")
	}
	if source == "" {
		return nil, errors.New("DATABASE_URL required")
	}
	return sql.Open("pgx", source)
}

func execute(args []string, out io.Writer, db func() (*sql.DB, error)) (int, error) {
	o, err := parse(args)
	if err != nil {
		return 1, err
	}
	conn, err := db()
	if err != nil {
		return 1, errors.New("lhk-sim database unavailable")
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	switch o.op {
	case "plan":
		p, err := simscenario.PlanScenario(ctx, conn)
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "plan", "plan": p})
	case "apply":
		rec, err := simscenario.Apply(ctx, conn, o.approve, o.actor, simscenario.NewRequestID(), time.Now())
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "apply", "record": rec})
	case "verify":
		rep, err := simscenario.Verify(ctx, conn, time.Now(), time.Duration(o.fresh)*time.Minute)
		if err != nil {
			return 1, err
		}
		if err = enc.Encode(map[string]any{"operation": "verify", "report": rep}); err != nil {
			return 1, err
		}
		fmt.Fprintf(out, "LHK_SIM_SCENARIO=%s REAL_HARDWARE=NONE\n", rep.Verdict)
		switch rep.Verdict {
		case "PASS":
			return 0, nil
		case "PENDING":
			return exitPending, nil
		}
		return exitFail, nil
	case "cleanup-plan":
		p, err := simscenario.PlanCleanup(ctx, conn, o.meterData)
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "cleanup-plan", "plan": p})
	default:
		p, err := simscenario.ApplyCleanup(ctx, conn, o.meterData, o.approve, o.actor, simscenario.NewRequestID())
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "cleanup-apply", "plan": p})
	}
}

func safeReason(err error) string {
	for _, known := range []error{simscenario.ErrDigestMismatch, simscenario.ErrNotReady, simscenario.ErrActorRequired} {
		if errors.Is(err, known) {
			return err.Error()
		}
	}
	msg := err.Error()
	for _, p := range []string{"usage:", "invalid arguments", "apply requires", "--approve/--actor", "--include-meter-data", "lhk-sim database unavailable"} {
		if strings.HasPrefix(msg, p) {
			return msg
		}
	}
	return "operation failed; check private DB configuration and retry (details withheld)"
}

func main() {
	code, err := execute(os.Args[1:], os.Stdout, openDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lhk-sim rejected: "+safeReason(err))
		os.Exit(1)
	}
	os.Exit(code)
}
