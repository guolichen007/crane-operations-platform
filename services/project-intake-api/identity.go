package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

var errIdentityInput = errors.New("identity input rejected")
var errIdentityDenied = errors.New("identity not authorized")
var identityUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var evidenceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{2,127}$`)

const identityLimit = 1 << 20
const identityLock = 727105

type IdentityTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type IdentityUser struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Team        string `json:"team"`
	Active      *bool  `json:"active"`
}
type IdentityManifest struct {
	ApprovalReference string         `json:"approvalReference"`
	Teams             []IdentityTeam `json:"teams"`
	Users             []IdentityUser `json:"users"`
}
type IdentitySummary struct {
	DryRun          bool   `json:"dry_run"`
	TeamsChanged    int64  `json:"teams_changed"`
	UsersChanged    int64  `json:"users_changed"`
	SessionsRevoked int64  `json:"sessions_revoked"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

// Reject ambiguous duplicate keys at every depth, before decoding the typed document.
func uniqueJSON(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return errIdentityInput
				}
				seen[s] = true
				if e = uniqueJSON(d); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = uniqueJSON(d); e != nil {
					return e
				}
			}
		default:
			return errIdentityInput
		}
		_, e = d.Token()
		return e
	}
	return nil
}
func identityText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) == s && len(s) > 0 && utf8.RuneCountInString(s) <= 128 && !strings.ContainsFunc(s, unicode.IsControl)
}
func validIdentityUUID(s string) bool {
	return identityUUID.MatchString(s) && s != "00000000-0000-0000-0000-000000000000"
}
func parseIdentity(b []byte) (IdentityManifest, error) {
	var m IdentityManifest
	if len(b) == 0 || len(b) > identityLimit || !utf8.Valid(b) {
		return m, errIdentityInput
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if uniqueJSON(d) != nil {
		return m, errIdentityInput
	}
	if _, e := d.Token(); e != io.EOF {
		return m, errIdentityInput
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return m, errIdentityInput
	}
	var fields struct {
		Users []map[string]json.RawMessage `json:"users"`
	}
	if json.Unmarshal(b, &fields) != nil {
		return m, errIdentityInput
	}
	for _, u := range fields.Users {
		team, ok := u["team"]
		if !ok || bytes.Equal(bytes.TrimSpace(team), []byte("null")) {
			return m, errIdentityInput
		}
	}
	if !evidenceID.MatchString(m.ApprovalReference) || m.Teams == nil || m.Users == nil || len(m.Teams) > 1000 || len(m.Users) > 5000 {
		return m, errIdentityInput
	}
	teams, subjects := map[string]bool{}, map[string]bool{}
	for _, t := range m.Teams {
		if !validIdentityUUID(t.ID) || !identityText(t.Name) || teams[t.ID] {
			return m, errIdentityInput
		}
		teams[t.ID] = true
	}
	for _, u := range m.Users {
		if !validIdentityUUID(u.Subject) || subjects[u.Subject] || !identityText(u.DisplayName) || u.Active == nil {
			return m, errIdentityInput
		}
		subjects[u.Subject] = true
		switch u.Role {
		case "baobei_sales", "baobei_manager":
			if !validIdentityUUID(u.Team) {
				return m, errIdentityInput
			}
		case "baobei_director", "baobei_admin":
			if u.Team != "" {
				return m, errIdentityInput
			}
		default:
			return m, errIdentityInput
		}
	}
	return m, nil
}
func applyIdentity(ctx context.Context, db *pgxpool.Pool, b []byte, operator string, dry bool) (IdentitySummary, error) {
	sum := IdentitySummary{DryRun: dry}
	m, e := parseIdentity(b)
	if e != nil || !evidenceID.MatchString(operator) {
		return sum, errIdentityInput
	}
	digest := sha256.Sum256(b)
	sum.ManifestSHA256 = hex.EncodeToString(digest[:])
	tx, e := db.Begin(ctx)
	if e != nil {
		return sum, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); e != nil {
		return sum, e
	}
	for _, t := range m.Teams {
		r, e := tx.Exec(ctx, `INSERT INTO teams(id,name) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET name=excluded.name,staff_version=teams.staff_version+1 WHERE teams.name IS DISTINCT FROM excluded.name`, t.ID, t.Name)
		if e != nil {
			return sum, e
		}
		sum.TeamsChanged += r.RowsAffected()
	}
	for _, u := range m.Users {
		var managed bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE subject=$1 AND staff_managed)`, u.Subject).Scan(&managed); e != nil {
			return sum, e
		}
		if managed {
			return sum, errIdentityDenied
		}
		if u.Team != "" {
			var exists bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, u.Team).Scan(&exists); e != nil {
				return sum, e
			}
			if !exists {
				return sum, errIdentityInput
			}
		}
		r, e := tx.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,active) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5)
   ON CONFLICT(subject) DO UPDATE SET display_name=excluded.display_name,role=excluded.role,team_id=excluded.team_id,active=excluded.active
   WHERE (app_users.display_name,app_users.role,app_users.team_id,app_users.active) IS DISTINCT FROM (excluded.display_name,excluded.role,excluded.team_id,excluded.active)`, u.Subject, u.DisplayName, u.Role, u.Team, *u.Active)
		if e != nil {
			return sum, e
		}
		sum.UsersChanged += r.RowsAffected()
		if r.RowsAffected() > 0 {
			r, e = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, u.Subject)
			if e != nil {
				return sum, e
			}
			sum.SessionsRevoked += r.RowsAffected()
		}
	}
	if dry {
		return sum, nil
	} // deferred rollback includes all writes and session revocations.
	if sum.TeamsChanged+sum.UsersChanged > 0 {
		details, e := json.Marshal(map[string]any{"operator": operator, "approval_reference": m.ApprovalReference, "manifest_sha256": sum.ManifestSHA256, "teams_changed": sum.TeamsChanged, "users_changed": sum.UsersChanged, "sessions_revoked": sum.SessionsRevoked})
		if e != nil {
			return sum, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO audit_events(subject,action,revision,details) VALUES($1,'IDENTITY_APPLY',0,$2::jsonb)`, operator, string(details)); e != nil {
			return sum, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return sum, e
	}
	return sum, nil
}
func identityCommand(ctx context.Context, db *pgxpool.Pool, args []string, out io.Writer) error {
	f := flag.NewFlagSet("identity-apply", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("file", "", "root-only JSON")
	operator := f.String("operator", "", "approved operator evidence ID")
	dry := f.Bool("dry-run", false, "validate without committing")
	if f.Parse(args) != nil || f.NArg() != 0 || *path == "" || !evidenceID.MatchString(*operator) {
		return errIdentityInput
	}
	b, e := readIdentityFile(*path)
	if e != nil {
		return errIdentityInput
	}
	sum, e := applyIdentity(ctx, db, b, *operator, *dry)
	if e != nil {
		return errors.New("identity apply failed; no changes committed")
	}
	if e = json.NewEncoder(out).Encode(sum); e != nil {
		return fmt.Errorf("identity summary unavailable")
	}
	return nil
}
