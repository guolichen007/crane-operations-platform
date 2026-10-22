package audit

import (
	"context"
	"database/sql"
)

func Attempt(ctx context.Context, db *sql.DB, user, device, action string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO audit_logs(user_id,device_id,action,result) VALUES($1,$2,$3,$4)", user, device, action, "attempt")
	return err
}
