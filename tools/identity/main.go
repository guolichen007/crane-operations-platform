// identity is an explicit host-administrator bootstrap tool, not a public permission endpoint.
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
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func execute(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: identity bind|grant|revoke|list-grants with explicit --id and project")
	}
	flags := flag.NewFlagSet("identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "local user ID")
	issuer := flags.String("issuer", "", "exact OIDC issuer")
	subject := flags.String("subject", "", "OIDC sub, never email")
	name := flags.String("display-name", "", "display metadata only")
	enabled := flags.Bool("enable", false, "explicitly enable identity")
	project := flags.String("project", "", "one explicit project")
	role := flags.String("role", "", "one project role")
	actor := flags.String("actor", "", "explicit host administrator audit identity")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *id == "" {
		return errors.New("invalid explicit identity arguments")
	}
	if args[0] != "list-grants" && *actor == "" {
		return errors.New("host administrator actor required")
	}
	if args[0] != "bind" && args[0] != "grant" && args[0] != "revoke" && args[0] != "list-grants" {
		return errors.New("unknown identity operation")
	}
	source := os.Getenv("DATABASE_URL")
	if path := os.Getenv("DATABASE_PASSWORD_FILE"); path != "" {
		password, err := platform.PrivateSecret(path, 20)
		if err != nil {
			return err
		}
		source, err = platform.DatabaseWithPassword(source, password)
		if err != nil {
			return err
		}
	} else if os.Getenv("APP_ENV") != "development" {
		return errors.New("identity requires a DB password file outside development")
	}
	db, err := sql.Open("pgx", source)
	if err != nil {
		return errors.New("identity database unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	event := auditlog.Event{Actor: "host:" + *actor, RequestID: platform.ID()}
	switch args[0] {
	case "bind":
		err = identity.Bind(ctx, db, *id, *issuer, *subject, *name, *enabled, event)
	case "grant":
		err = identity.ChangeGrant(ctx, db, *id, *project, *role, false, event)
	case "revoke":
		err = identity.ChangeGrant(ctx, db, *id, *project, "", true, event)
	case "list-grants":
		grants, e := identity.Grants(ctx, db, *id)
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(grants)
	}
	if err == nil {
		_, err = fmt.Fprintln(output, "Identity operation completed; no credentials printed.")
	}
	return err
}

func main() {
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Identity operation rejected; check explicit identity, project and private DB configuration.")
		os.Exit(1)
	}
}
