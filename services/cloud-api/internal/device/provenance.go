package device

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

var (
	ErrRuntimeNotRegistered      = errors.New("fleet runtime must be preregistered")
	ErrRuntimeProvenanceMismatch = errors.New("fleet runtime database provenance mismatch")
)

// ValidateFleet checks every explicit descriptor in one consistent DB snapshot.
// An absent nullable asset differs from a registered asset; neither is a fallback.
func ValidateFleet(ctx context.Context, db *sql.DB, manifest contract.FleetManifest) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, runtime := range manifest.Devices {
		var project, source, tenant string
		var asset sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT d.project_id,d.source_mode,d.asset_device_id,p.tenant_id
FROM devices d JOIN projects p ON p.id=d.project_id WHERE d.id=$1`, runtime.RuntimeDeviceId).Scan(&project, &source, &asset, &tenant)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fleet entry %d: %w", i+1, ErrRuntimeNotRegistered)
		}
		if err != nil {
			return err
		}
		if project != runtime.ProjectId || source != runtime.SourceMode || tenant != manifest.TenantId ||
			asset.Valid != (runtime.AssetDeviceId != nil) || (asset.Valid && asset.String != *runtime.AssetDeviceId) {
			return fmt.Errorf("fleet entry %d: %w", i+1, ErrRuntimeProvenanceMismatch)
		}
	}
	return tx.Commit()
}
