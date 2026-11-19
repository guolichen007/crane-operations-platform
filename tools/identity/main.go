// identity is an explicit host-administrator bootstrap and recovery tool, not a public permission endpoint.
//
// Operations:
//
//	bind | grant | revoke | list-grants      (unchanged OIDC identity / project grant bootstrap)
//	adopt-admin  --username U [--display-name N]
//	             make an EXISTING realm login (e.g. the owner's account) a managed person holding
//	             system_admin; keeps the local id, grants and history; never touches the password.
//	             Needs the account service (ACCOUNT_SERVICE_SECRET_FILE, OIDC_ISSUER, KEYCLOAK_INTERNAL_URL).
//	list-admins  enabled/disabled system administrators (no secrets).
//	system-role  --id U --role R [--remove]   (host recovery; still refuses to remove the last admin)
//	protect      --id U --reason OWNER|SERVICE|SYSTEM | --clear   (R9: never archivable / purgeable)
//	mark-test    --id U --username N [--clear]   mark (or clear) an explicitly named account as a
//	             test account; only marked test accounts can ever be purged (never OWNER / SERVICE /
//	             SYSTEM, never a system_admin); audited
//	cleanup-plan --list FILE      read-only classification of an EXPLICIT test-account list
//	             ({"reason": "...", "accounts": [{"id": "...", "username": "..."}]}); never a pattern
//	cleanup-apply --list FILE --approve DIGEST   archive (+ purge when unreferenced) exactly the
//	             approved plan through the normal service guards; needs the account service.
//
// Every write is audited as host:<actor>.
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

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
	"github.com/guolichen007/crane-operations-platform/internal/personnel"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var operations = map[string]bool{"bind": true, "grant": true, "revoke": true, "list-grants": true, "adopt-admin": true, "list-admins": true, "system-role": true,
	"protect": true, "cleanup-plan": true, "cleanup-apply": true, "mark-test": true}

func execute(args []string, output io.Writer) error {
	if len(args) == 0 || !operations[args[0]] {
		return errors.New("usage: identity bind|grant|revoke|list-grants|adopt-admin|list-admins|system-role|protect|mark-test|cleanup-plan|cleanup-apply")
	}
	flags := flag.NewFlagSet("identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "local user ID")
	issuer := flags.String("issuer", "", "exact OIDC issuer")
	subject := flags.String("subject", "", "OIDC sub, never email")
	name := flags.String("display-name", "", "display metadata only")
	enabled := flags.Bool("enable", false, "explicitly enable identity")
	project := flags.String("project", "", "one explicit project")
	role := flags.String("role", "", "one project or system role")
	remove := flags.Bool("remove", false, "remove the system role")
	username := flags.String("username", "", "exact Keycloak username (adopt-admin)")
	actor := flags.String("actor", "", "explicit host administrator audit identity")
	reason := flags.String("reason", "", "protection reason (protect)")
	clear := flags.Bool("clear", false, "clear the protection (protect)")
	list := flags.String("list", "", "explicit cleanup list file (cleanup-plan / cleanup-apply)")
	approve := flags.String("approve", "", "approved cleanup plan digest (cleanup-apply)")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid explicit identity arguments")
	}
	op := args[0]
	needsID := op == "bind" || op == "grant" || op == "revoke" || op == "list-grants" || op == "system-role" || op == "protect" || op == "mark-test"
	if (needsID && *id == "") || ((op == "adopt-admin" || op == "mark-test") && *username == "") || (strings.HasPrefix(op, "cleanup-") && *list == "") ||
		(op == "cleanup-apply" && *approve == "") || (op == "protect" && (*reason == "") == !*clear) {
		return errors.New("invalid explicit identity arguments")
	}
	if op != "list-grants" && op != "list-admins" && op != "cleanup-plan" && *actor == "" {
		return errors.New("host administrator actor required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	event := auditlog.Event{Actor: "host:" + *actor, RequestID: platform.ID()}
	host := personnel.Actor{ID: *actor, Host: true, Event: event}
	svc := &personnel.Service{DB: db, Issuer: os.Getenv("OIDC_ISSUER")}
	switch op {
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
	case "list-admins":
		rows, e := db.QueryContext(ctx, `SELECT u.id, COALESCE(u.username,''), u.display_name, u.enabled FROM system_memberships m JOIN users u ON u.id=m.user_id WHERE m.role='system_admin' ORDER BY u.id`)
		if e != nil {
			return e
		}
		defer rows.Close()
		type admin struct {
			ID, Username, DisplayName string
			Enabled                   bool
		}
		out := []admin{}
		for rows.Next() {
			var a admin
			if e = rows.Scan(&a.ID, &a.Username, &a.DisplayName, &a.Enabled); e != nil {
				return e
			}
			out = append(out, a)
		}
		return json.NewEncoder(output).Encode(out)
	case "protect":
		err = svc.SetProtected(ctx, host, *id, *reason)
	case "mark-test":
		err = svc.MarkTestAccount(ctx, host, *id, *username, !*clear)
	case "cleanup-plan", "cleanup-apply":
		raw, e := os.ReadFile(*list)
		if e != nil {
			return errors.New("cleanup list unreadable")
		}
		var doc personnel.CleanupList
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if e = dec.Decode(&doc); e != nil {
			return errors.New("cleanup list must be {reason, accounts:[{id, username}]}")
		}
		if op == "cleanup-plan" {
			plan, e := svc.PlanCleanup(ctx, doc)
			if e != nil {
				return e
			}
			enc := json.NewEncoder(output)
			enc.SetIndent("", "  ")
			return enc.Encode(plan)
		}
		if svc.IdP, e = accountClient(svc.Issuer); e != nil {
			return e
		}
		plan, e := svc.ApplyCleanup(ctx, host, doc, *approve)
		if e != nil {
			return e
		}
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if e = enc.Encode(plan); e != nil {
			return e
		}
	case "adopt-admin":
		if !platform.SecureURL(svc.Issuer, true) {
			return errors.New("OIDC_ISSUER required")
		}
		secret, e := platform.PrivateSecret(os.Getenv("ACCOUNT_SERVICE_SECRET_FILE"), 32)
		if e != nil {
			return e
		}
		client, e := kcadmin.New(svc.Issuer, os.Getenv("KEYCLOAK_INTERNAL_URL"), envOr("ACCOUNT_SERVICE_CLIENT_ID", "crane-account-service"), secret, nil)
		if e != nil {
			return e
		}
		svc.IdP = client
		p, e := svc.AdoptAdministrator(ctx, host, *username, *name)
		if e != nil {
			return e
		}
		_, err = fmt.Fprintf(output, "System administrator ready: id=%s username=%s enabled=%t; no credentials printed.\n", p.ID, p.Username, p.Enabled)
		return err
	case "system-role":
		if !personnel.ValidSystemRole(*role) {
			return errors.New("unknown system role")
		}
		var all []string
		rows, e := db.QueryContext(ctx, `SELECT role FROM system_memberships WHERE user_id=$1 ORDER BY role`, *id)
		if e != nil {
			return e
		}
		for rows.Next() {
			var r string
			if e = rows.Scan(&r); e != nil {
				rows.Close()
				return e
			}
			all = append(all, r)
		}
		rows.Close()
		want := []string{}
		for _, r := range all {
			if r != *role {
				want = append(want, r)
			}
		}
		if !*remove {
			want = append(want, *role)
		}
		if all == nil {
			all = []string{}
		}
		_, err = svc.SetSystemRoles(ctx, host, *id, want, all)
	}
	if err == nil {
		_, err = fmt.Fprintln(output, "Identity operation completed; no credentials printed.")
	}
	return err
}

// accountClient builds the realm-scoped account service client (cleanup-apply needs Keycloak to
// disable / delete the managed test accounts).
func accountClient(issuer string) (*kcadmin.Client, error) {
	if !platform.SecureURL(issuer, true) {
		return nil, errors.New("OIDC_ISSUER required")
	}
	secret, err := platform.PrivateSecret(os.Getenv("ACCOUNT_SERVICE_SECRET_FILE"), 32)
	if err != nil {
		return nil, err
	}
	return kcadmin.New(issuer, os.Getenv("KEYCLOAK_INTERNAL_URL"), envOr("ACCOUNT_SERVICE_CLIENT_ID", "crane-account-service"), secret, nil)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		msg := "Identity operation rejected; check explicit identity, project and private DB configuration."
		switch {
		case errors.Is(err, personnel.ErrLastAdmin):
			msg = "Rejected: the last enabled system administrator must remain."
		case errors.Is(err, personnel.ErrNotFound):
			msg = "Rejected: no such user (Keycloak username or local id)."
		case errors.Is(err, personnel.ErrIdentityProvider):
			msg = "Rejected: account service / Keycloak unavailable or not permitted."
		case errors.Is(err, personnel.ErrConflict):
			msg = "Rejected: conflicting identity or role state (re-plan if the cleanup plan changed)."
		case errors.Is(err, personnel.ErrProtectedIdentity):
			msg = "Rejected: protected identity (OWNER / SERVICE / SYSTEM)."
		case errors.Is(err, personnel.ErrNotTestAccount):
			msg = "Rejected: not a marked test account (identity mark-test --id U --username N first)."
		case errors.Is(err, personnel.ErrInvalid):
			msg = "Rejected: " + err.Error()
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
}
