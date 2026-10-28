// Package auditlog writes selected structured security facts, never request bodies or credentials.
package auditlog

import (
	"context"
	"database/sql"
)

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Event struct {
	Actor, Project, Device, Action, Result, SourceIP, RequestID string
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
	_, err := db.ExecContext(ctx, `INSERT INTO audit_logs(user_id,project_id,device_id,action,result,source_ip,request_id)
VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,$5,NULLIF($6,'')::inet,$7)`, event.Actor, event.Project, event.Device, event.Action, event.Result, event.SourceIP, event.RequestID)
	return err
}
