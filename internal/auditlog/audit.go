// Package auditlog writes selected structured security facts, never request bodies or credentials.
package auditlog

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Event is one security fact. TargetUser / Scope / Change were added with Schema 13 for the
// personnel domain: the person an action was applied to, the permission scope it touched
// ("system", "portfolio:<id>", "project:<id>", "account") and a structured, credential-free
// summary of the change. They are optional; older callers leave them empty (stored as NULL).
type Event struct {
	Actor, Project, Device, Action, Result, SourceIP, RequestID string
	TargetUser, Scope                                           string
	Change                                                      map[string]any
}

type eventKey struct{}

// WithEvent lets a business repository commit its mutation and security audit atomically.
func WithEvent(ctx context.Context, event Event) context.Context {
	return context.WithValue(ctx, eventKey{}, event)
}

func Commit(ctx context.Context, tx *sql.Tx) error {
	if event, ok := ctx.Value(eventKey{}).(Event); ok {
		if err := Write(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func Write(ctx context.Context, db Executor, event Event) error {
	var change any
	if len(event.Change) > 0 {
		b, err := json.Marshal(event.Change)
		if err != nil {
			return err
		}
		change = string(b)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO audit_logs(user_id,project_id,device_id,action,result,source_ip,request_id,target_user_id,scope,change)
VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,$5,NULLIF($6,'')::inet,$7,NULLIF($8,''),NULLIF($9,''),$10::jsonb)`,
		event.Actor, event.Project, event.Device, event.Action, event.Result, event.SourceIP, event.RequestID, event.TargetUser, event.Scope, change)
	return err
}
