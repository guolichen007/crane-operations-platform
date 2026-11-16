package asset

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// R6-BIZ-R1 F-03: read-only current installation location and installation
// history, derived solely from asset_installations (valid_to IS NULL = current,
// at most one by the asset_installations_one_active unique index) joined to
// asset_locations. Nothing here writes; coordinates are never part of the
// readback, so no longitude/latitude or regional centre can be presented as a
// workshop position.

// maxLocationDepth bounds the ancestor walk; a well-formed tree is
// SITE→PLANT→WORKSHOP→BAY→RUNWAY (5), the margin tolerates OTHER levels, and the
// bound also terminates a corrupt parent cycle instead of looping.
const maxLocationDepth = 16

// ErrLocationTreeCorrupt is returned when an ancestor walk exceeds the bound.
var ErrLocationTreeCorrupt = errors.New("location hierarchy exceeds the supported depth")

// locationPath returns the root→leaf path of one location (leaf included). The
// composite (parent_id, project_id) FK keeps every ancestor in the same project;
// the project predicate is repeated so a path can never leave it.
func locationPath(ctx context.Context, tx *sql.Tx, locationID, projectID string) ([]contract.AssetLocationPathNode, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE up(id, parent_id, kind, code, name, status, depth) AS (
	  SELECT id, parent_id, kind, code, name, status, 0 FROM asset_locations WHERE id=$1 AND project_id=$2
	  UNION ALL
	  SELECT l.id, l.parent_id, l.kind, l.code, l.name, l.status, up.depth+1
	    FROM asset_locations l JOIN up ON l.id = up.parent_id
	   WHERE l.project_id=$2 AND up.depth < $3
	)
	SELECT id, kind, code, name, status, depth FROM up ORDER BY depth DESC`, locationID, projectID, maxLocationDepth+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	path := []contract.AssetLocationPathNode{}
	for rows.Next() {
		var n contract.AssetLocationPathNode
		var depth int
		if err := rows.Scan(&n.Id, &n.Kind, &n.Code, &n.Name, &n.Status, &depth); err != nil {
			return nil, err
		}
		if depth > maxLocationDepth {
			return nil, ErrLocationTreeCorrupt
		}
		path = append(path, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("location %s missing from project %s", locationID, projectID)
	}
	return path, nil
}

// readSnapshot opens one read-only REPEATABLE READ transaction, so the asset row,
// the installation rows and every path come from a single snapshot even while a
// MoveAsset commits concurrently.
func readSnapshot(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
}

// GetAssetLocation returns the asset's current installation location, or a nil
// Current when the asset has never been installed (an honest empty state). A
// retired asset keeps its readback. ErrNotFound when the asset does not exist.
func GetAssetLocation(ctx context.Context, db *sql.DB, assetID string) (contract.AssetLocationReadback, error) {
	out := contract.AssetLocationReadback{AssetId: assetID}
	tx, err := readSnapshot(ctx, db)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT project_id, asset_status FROM asset_devices WHERE id=$1`, assetID).Scan(&out.ProjectId, &out.AssetStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	var cur contract.AssetCurrentLocation
	err = tx.QueryRowContext(ctx, `SELECT i.id, l.id, l.code, l.name, l.kind, l.status, i.valid_from
	  FROM asset_installations i JOIN asset_locations l ON l.id = i.location_id
	 WHERE i.asset_id=$1 AND i.valid_to IS NULL AND l.project_id=$2`, assetID, out.ProjectId).
		Scan(&cur.InstallationId, &cur.LocationId, &cur.Code, &cur.Name, &cur.Kind, &cur.Status, &cur.ValidFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return out, tx.Commit()
	}
	if err != nil {
		return out, err
	}
	cur.ValidFrom = cur.ValidFrom.UTC()
	if cur.Path, err = locationPath(ctx, tx, cur.LocationId, out.ProjectId); err != nil {
		return out, err
	}
	out.Current = &cur
	return out, tx.Commit()
}

// ListAssetInstallationHistory returns the asset's installation records, newest
// first (valid_from DESC, id DESC as a stable tie-break), paged with the same
// 1..100 window as every list endpoint. A retired asset keeps its history.
func ListAssetInstallationHistory(ctx context.Context, db *sql.DB, assetID string, opts ListOptions) (contract.AssetInstallationHistory, error) {
	limit, offset := opts.normalized()
	out := contract.AssetInstallationHistory{AssetId: assetID, Limit: limit, Offset: offset, Items: []contract.AssetInstallationRecord{}}
	tx, err := readSnapshot(ctx, db)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, assetID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_installations i JOIN asset_locations l ON l.id=i.location_id WHERE i.asset_id=$1 AND l.project_id=$2`, assetID, projectID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id, l.id, l.code, l.name, l.kind, l.status, i.valid_from, i.valid_to
	  FROM asset_installations i JOIN asset_locations l ON l.id = i.location_id
	 WHERE i.asset_id=$1 AND l.project_id=$2
	 ORDER BY i.valid_from DESC, i.id DESC LIMIT $3 OFFSET $4`, assetID, projectID, limit, offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r contract.AssetInstallationRecord
		var validTo sql.NullTime
		if err := rows.Scan(&r.InstallationId, &r.LocationId, &r.Code, &r.Name, &r.Kind, &r.Status, &r.ValidFrom, &validTo); err != nil {
			rows.Close()
			return out, err
		}
		r.ValidFrom = r.ValidFrom.UTC()
		if validTo.Valid {
			t := validTo.Time.UTC()
			r.ValidTo = &t
		}
		r.Current = !validTo.Valid
		out.Items = append(out.Items, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for i := range out.Items {
		if out.Items[i].Path, err = locationPath(ctx, tx, out.Items[i].LocationId, projectID); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
