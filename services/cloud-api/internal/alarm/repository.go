package alarm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

func Record(ctx context.Context, db *sql.DB, m contract.Envelope) error {
	table := "events"
	if m.Kind == "ALARM" {
		table = "alarms"
	} else if m.Kind != "EVENT" {
		return errors.New("invalid event kind")
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	_, e = db.ExecContext(ctx, "INSERT INTO "+table+"(message_id,device_id,observed_at,payload_json) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", m.MessageId, m.DeviceId, m.SentAt, b)
	return e
}
func List(ctx context.Context, db *sql.DB) ([]contract.Envelope, error) {
	rows, e := db.QueryContext(ctx, "SELECT payload_json FROM alarms ORDER BY observed_at DESC LIMIT 100")
	return readRows(rows, e)
}

// Scope is applied before LIMIT using the authoritative device project in PostgreSQL.
func ListScoped(ctx context.Context, db *sql.DB, user, device string) ([]contract.Envelope, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.payload_json FROM alarms a JOIN devices d ON d.id=a.device_id
JOIN project_memberships m ON m.project_id=d.project_id JOIN users u ON u.id=m.user_id
WHERE m.user_id=$1 AND u.enabled AND m.role IN ('viewer','operator','maintainer','platform_admin')
AND ($2='' OR a.device_id=$2) ORDER BY a.observed_at DESC LIMIT 100`, user, device)
	return readRows(rows, err)
}

func readRows(rows *sql.Rows, e error) ([]contract.Envelope, error) {
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.Envelope{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var m contract.Envelope
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
