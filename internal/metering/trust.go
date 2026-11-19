package metering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// CounterResetInput confirms that the counter really restarted at the PLC (replaced module,
// cleared DB block …). Only an administrator decides this; a process restart never does.
type CounterResetInput struct {
	SourceMode string `json:"sourceMode"`
	Reason     string `json:"reason"`
}

// ConfirmCounterReset resolves a DECREASE_PENDING meter as a real counter reset: the usage
// counted since the reset (pending_last_raw, the counter restarted from 0) is added once, and the
// trusted mark becomes the current raw value. Recorded in meter_counter_events + audit. A meter
// that is not pending is a conflict (nothing to confirm).
func ConfirmCounterReset(ctx context.Context, db *sql.DB, assetID, meterID string, in CounterResetInput, actor, requestID string) error {
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) < 2 || len([]rune(reason)) > 200 {
		return fmt.Errorf("%w: reason (2–200 characters) required", ErrInvalid)
	}
	switch in.SourceMode {
	case "REAL", "SIMULATION", "TEST":
	default:
		return fmt.Errorf("%w: sourceMode REAL | SIMULATION | TEST", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var project string
	if err = tx.QueryRowContext(ctx, `SELECT a.project_id FROM meter_definitions d JOIN asset_devices a ON a.id=d.asset_id WHERE d.id=$1 AND d.asset_id=$2 AND d.meter_kind='CUMULATIVE'`, meterID, assetID).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var trust string
	var since sql.NullTime
	var first, lastPending sql.NullFloat64
	var acc float64
	err = tx.QueryRowContext(ctx, `SELECT trust_state, pending_since, pending_first_raw, pending_last_raw, accumulated FROM meter_states WHERE meter_id=$1 AND source_mode=$2 FOR UPDATE`, meterID, in.SourceMode).
		Scan(&trust, &since, &first, &lastPending, &acc)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if trust != "DECREASE_PENDING" || !since.Valid || !first.Valid || !lastPending.Valid {
		return fmt.Errorf("%w: the meter has no pending decrease to confirm", ErrConflict)
	}
	// After a reset the counter starts from 0: the usage since the reset is the latest pending
	// raw value (the smallest one seen is the evidence of the reset, kept in the event).
	after := acc + lastPending.Float64
	if _, err = tx.ExecContext(ctx, `INSERT INTO meter_counter_events(id,meter_id,source_mode,event_kind,pending_since,pending_first_raw,pending_last_raw,accumulated_before,accumulated_after,reason,actor,request_id)
VALUES($1,$2,$3,'RESET_CONFIRMED',$4,$5,$6,$7,$8,$9,$10,$11)`, platform.ID(), meterID, in.SourceMode, since.Time, first.Float64, lastPending.Float64, acc, after, reason, actor, requestID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE meter_states SET last_raw_value=pending_last_raw, accumulated=$3, trust_state='OK', pending_since=NULL, pending_first_raw=NULL, pending_last_raw=NULL, updated_at=now()
WHERE meter_id=$1 AND source_mode=$2`, meterID, in.SourceMode, after); err != nil {
		return err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "meter_counter_reset_confirmed", Result: "success:" + in.SourceMode, RequestID: requestID,
		Change: map[string]any{"meter": meterID, "accumulatedBefore": acc, "accumulatedAfter": after, "reason": reason}}); err != nil {
		return err
	}
	return tx.Commit()
}
