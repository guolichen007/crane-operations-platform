// onboarding is the explicit host-administrator tool that registers the 老河口 first real asset
// (LHK-CRANE-001) from the staging document compiled into this binary (R9 A4/A5). It is not an
// HTTP endpoint.
//
//	onboarding plan   [--staging FILE] [--xlsx FILE]
//	onboarding apply  --approve <plan digest> --actor <admin id> [--staging FILE]
//	onboarding verify [--staging FILE]
//
// plan / verify are read-only. apply writes only NEW objects in one transaction and refuses unless
// --approve equals the digest of the current plan and the plan has no conflict. --staging uses an
// explicit document instead of the embedded one (it must still be the reviewed format); --xlsx
// recomputes the SHA256 of the original 老河口行车监控平台点位.xlsx and refuses a mismatch with the
// staging provenance. Output is JSON and never contains the database URL or password.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const exitDrift = 3

type options struct {
	op, approve, actor, staging, xlsx string
}

func parse(args []string) (options, error) {
	if len(args) == 0 {
		return options{}, errors.New("usage: onboarding plan|apply|verify [--staging FILE] [--xlsx FILE] [--approve DIGEST --actor ID]")
	}
	o := options{op: args[0]}
	if o.op != "plan" && o.op != "apply" && o.op != "verify" {
		return options{}, errors.New("unknown operation")
	}
	flags := flag.NewFlagSet("onboarding", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	approve := flags.String("approve", "", "digest of the approved plan (apply only)")
	actor := flags.String("actor", "", "explicit host administrator identity (apply only)")
	staging := flags.String("staging", "", "explicit staging document (default: embedded)")
	xlsx := flags.String("xlsx", "", "original spreadsheet to check against the staging SHA256")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return options{}, errors.New("invalid arguments")
	}
	o.approve, o.actor, o.staging, o.xlsx = strings.TrimSpace(*approve), strings.TrimSpace(*actor), *staging, *xlsx
	if o.op == "apply" && (o.approve == "" || o.actor == "") {
		return options{}, errors.New("apply requires --approve <plan digest> and --actor <administrator>")
	}
	if o.op != "apply" && (o.approve != "" || o.actor != "") {
		return options{}, errors.New("--approve/--actor are only accepted by apply")
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
		return nil, errors.New("onboarding requires a DB password file outside development")
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
	staging, _ := onboarding.Embedded()
	if o.staging != "" {
		if staging, err = os.ReadFile(o.staging); err != nil {
			return 1, errors.New("staging document unreadable")
		}
	}
	doc, err := onboarding.Parse(staging)
	if err != nil {
		return 1, err
	}
	if o.xlsx != "" {
		b, err := os.ReadFile(o.xlsx)
		if err != nil {
			return 1, errors.New("xlsx unreadable")
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != doc.Source.SHA256 {
			return 1, fmt.Errorf("xlsx SHA256 %s does not match the staging provenance %s", got, doc.Source.SHA256)
		}
	}
	db, err := openDB()
	if err != nil {
		return 1, errors.New("onboarding database unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	switch o.op {
	case "plan":
		p, _, err := onboarding.PlanImport(ctx, db, staging)
		if err != nil {
			return 1, err
		}
		p.Items = nil // counts + conflicts are the review surface; the full list stays in verify output on demand
		return 0, enc.Encode(map[string]any{"operation": "plan", "plan": p, "xlsxChecked": o.xlsx != ""})
	case "apply":
		rec, err := onboarding.Apply(ctx, db, staging, o.approve, o.actor, platform.ID())
		if err != nil {
			return 1, err
		}
		return 0, enc.Encode(map[string]any{"operation": "apply", "record": rec})
	default:
		r, err := onboarding.Verify(ctx, db, staging)
		if err != nil {
			return 1, err
		}
		r.Plan.Items = nil
		if err := enc.Encode(map[string]any{"operation": "verify", "report": r}); err != nil {
			return 1, err
		}
		if !r.Consistent {
			return exitDrift, nil
		}
		return 0, nil
	}
}

func safeReason(err error) string {
	for _, known := range []error{onboarding.ErrDigestMismatch, onboarding.ErrConflict, onboarding.ErrActorRequired, onboarding.ErrStaging} {
		if errors.Is(err, known) {
			return err.Error()
		}
	}
	msg := err.Error()
	for _, p := range []string{"usage:", "unknown operation", "invalid arguments", "apply requires", "--approve/--actor", "xlsx", "staging document", "onboarding database unavailable"} {
		if strings.HasPrefix(msg, p) {
			return msg
		}
	}
	return "operation failed; check private DB configuration and retry (details withheld)"
}

func main() {
	code, err := execute(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Onboarding rejected: "+safeReason(err))
		os.Exit(1)
	}
	os.Exit(code)
}
