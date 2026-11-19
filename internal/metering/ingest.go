// Package metering persists device meter batches (R9 B3) and evaluates maintenance schedules (B4).
//
// Ingest rules:
//   - a batch is accepted only from a runtime whose devices row is bound to an asset; its
//     source_mode (REAL / SIMULATION / TEST) comes from the devices row, NEVER from the message,
//     and a payload claiming another mode is refused (a simulator can never write REAL meters);
//   - (meter, mode, runtime, bootId, sequence) is unique: duplicates are no-ops (idempotent);
//   - counting happens at the PLC / edge; the cloud persists the cumulative value and its history:
//     value ≥ last (same boot)          → delta = value - last
//     value < last (same boot)          → DECREASE, delta 0 (never subtracts)
//     new bootId and value < last       → COUNTER_RESET, delta = value (the counter restarted)
//     older (bootSeq, sequence) arriving late → OUT_OF_ORDER, stored, does not move the state
//     value NULL / quality MISSING      → MISSING row, delta 0, last value kept (never zero)
//     quality BAD                       → QUALITY row, delta 0
//     so `accumulated` only ever grows: a network outage, a restart or a counter reset never
//     zeroes a real cumulative value; back-filled samples (spool replay) are applied in order;
//   - daily metrics keep the latest value of the day (by sequence); fault events are unique by
//     (mode, runtime, eventId).
package metering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

var (
	ErrNotBound     = errors.New("runtime is not bound to an asset")
	ErrModeMismatch = errors.New("payload sourceMode differs from the registered runtime mode")
)

// Result summarizes one ingested batch.
type Result struct {
	AssetID    string `json:"assetId"`
	SourceMode string `json:"sourceMode"`
	Stored     int    `json:"stored"`
	Duplicates int    `json:"duplicates"`
	Unknown    int    `json:"unknown"` // samples for points / metrics without a meter definition
	Faults     int    `json:"faults"`
	Anomalies  map[string]int
}

type meterRef struct {
	id, kind string
}

// Ingest stores one METER envelope in one transaction (all or nothing; the caller withholds the
// application ACK on error, so the edge keeps the batch in its spool and retries).
func Ingest(ctx context.Context, db *sql.DB, m contract.Envelope) (Result, error) {
	p := contract.Payload[contract.MeterBatchPayload](m)
	res := Result{Anomalies: map[string]int{}}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var asset sql.NullString
	var mode string
	err = tx.QueryRowContext(ctx, `SELECT asset_device_id, source_mode FROM devices WHERE id=$1 AND project_id=$2`, m.DeviceId, m.ProjectId).Scan(&asset, &mode)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !asset.Valid) {
		return res, ErrNotBound
	}
	if err != nil {
		return res, err
	}
	if p.SourceMode != mode {
		return res, fmt.Errorf("%w: runtime %s is %s, payload says %s", ErrModeMismatch, m.DeviceId, mode, p.SourceMode)
	}
	res.AssetID, res.SourceMode = asset.String, mode
	refs := map[string]meterRef{}
	rows, err := tx.QueryContext(ctx, `SELECT p.point_code, d.metric, d.id, d.meter_kind FROM meter_definitions d JOIN telemetry_points p ON p.id=d.point_id WHERE d.asset_id=$1 AND d.status <> 'DISABLED'`, asset.String)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var point, metric string
		var r meterRef
		if err = rows.Scan(&point, &metric, &r.id, &r.kind); err != nil {
			rows.Close()
			return res, err
		}
		refs[point+"|"+metric] = r
	}
	rows.Close()
	sampled := p.SampledAt
	if sampled.IsZero() {
		sampled = m.SentAt
	}
	for _, s := range p.Samples {
		r, ok := refs[s.PointCode+"|"+s.Metric]
		if !ok {
			res.Unknown++
			continue
		}
		stored, anomaly, err := apply(ctx, tx, r, mode, m, sampled, s)
		if err != nil {
			return res, err
		}
		if !stored {
			res.Duplicates++
			continue
		}
		res.Stored++
		if anomaly != "" {
			res.Anomalies[anomaly]++
		}
	}
	for _, f := range p.Faults {
		var point sql.NullString
		_ = tx.QueryRowContext(ctx, `SELECT id FROM telemetry_points WHERE asset_id=$1 AND point_code=$2`, asset.String, f.PointCode).Scan(&point)
		r, err := tx.ExecContext(ctx, `INSERT INTO fault_events(asset_id,point_id,source_mode,runtime_device_id,event_id,code,text,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
			asset.String, point, mode, m.DeviceId, f.EventId, f.Code, f.Text, f.OccurredAt)
		if err != nil {
			return res, err
		}
		if n, _ := r.RowsAffected(); n == 1 {
			res.Faults++
		}
	}
	return res, tx.Commit()
}

func apply(ctx context.Context, tx *sql.Tx, r meterRef, mode string, m contract.Envelope, sampled time.Time, s contract.MeterSample) (bool, string, error) {
	// State row (created on first sample), locked for this meter + mode.
	if _, err := tx.ExecContext(ctx, `INSERT INTO meter_states(meter_id,source_mode,runtime_device_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, r.id, mode, m.DeviceId); err != nil {
		return false, "", err
	}
	var last sql.NullFloat64
	var lastBoot sql.NullString
	var lastBootSeq, lastSeq sql.NullInt64
	var acc float64
	if err := tx.QueryRowContext(ctx, `SELECT last_raw_value, last_boot_id, last_boot_seq, last_sequence, accumulated FROM meter_states WHERE meter_id=$1 AND source_mode=$2 FOR UPDATE`, r.id, mode).
		Scan(&last, &lastBoot, &lastBootSeq, &lastSeq, &acc); err != nil {
		return false, "", err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM meter_readings WHERE meter_id=$1 AND source_mode=$2 AND runtime_device_id=$3 AND boot_id=$4 AND sequence=$5)`,
		r.id, mode, m.DeviceId, m.BootId, m.Sequence).Scan(&exists); err != nil {
		return false, "", err
	}
	if exists {
		return false, "", nil // duplicate delivery: idempotent
	}
	delta, anomaly, advance := 0.0, "", true
	quality := s.Quality
	switch {
	case s.Value == nil || quality == "MISSING":
		quality, anomaly, advance = "MISSING", "GAP", false
	case quality == "BAD":
		anomaly, advance = "QUALITY", false
	case lastSeq.Valid && (m.BootSeq < lastBootSeq.Int64 || (m.BootSeq == lastBootSeq.Int64 && m.BootId == lastBoot.String && m.Sequence < lastSeq.Int64)):
		anomaly, advance = "OUT_OF_ORDER", false // late sample: stored as history, never rewinds the state
	case r.kind == "DAILY" || r.kind == "STATE":
		// Daily / state values are not accumulated here.
	case !last.Valid:
		anomaly = "FIRST" // first reading establishes the base; no usage is invented before it
	case m.BootId != lastBoot.String && *s.Value < last.Float64:
		delta, anomaly = *s.Value, "COUNTER_RESET"
	case *s.Value < last.Float64:
		anomaly = "DECREASE"
	default:
		delta = *s.Value - last.Float64
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta < 0 {
		delta = 0
	}
	if r.kind == "CUMULATIVE" {
		acc += delta
	}
	var raw any
	if s.Value != nil {
		raw = *s.Value
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meter_readings(meter_id,source_mode,runtime_device_id,message_id,boot_id,boot_seq,sequence,sampled_at,raw_value,quality,delta,accumulated,anomaly)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''))`, r.id, mode, m.DeviceId, m.MessageId, m.BootId, m.BootSeq, m.Sequence, sampled, raw, quality, delta, acc, anomaly); err != nil {
		return false, "", err
	}
	if advance {
		if _, err := tx.ExecContext(ctx, `UPDATE meter_states SET last_raw_value=$3, last_boot_id=$4, last_boot_seq=$5, last_sequence=$6, last_sampled_at=$7, accumulated=$8, last_quality=$9, readings=readings+1, updated_at=now()
WHERE meter_id=$1 AND source_mode=$2`, r.id, mode, *s.Value, m.BootId, m.BootSeq, m.Sequence, sampled, acc, quality); err != nil {
			return false, "", err
		}
		if r.kind == "DAILY" {
			day := sampled.In(shanghai).Format("2006-01-02")
			if _, err := tx.ExecContext(ctx, `INSERT INTO daily_metric_values(meter_id,source_mode,day,value,last_boot_id,last_sequence) VALUES($1,$2,$3::date,$4,$5,$6)
ON CONFLICT (meter_id,source_mode,day) DO UPDATE SET value=excluded.value,last_boot_id=excluded.last_boot_id,last_sequence=excluded.last_sequence,updated_at=now()`,
				r.id, mode, day, *s.Value, m.BootId, m.Sequence); err != nil {
				return false, "", err
			}
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE meter_states SET last_quality=$3, readings=readings+1, updated_at=now() WHERE meter_id=$1 AND source_mode=$2`, r.id, mode, quality); err != nil {
		return false, "", err
	}
	return true, anomaly, nil
}

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()
