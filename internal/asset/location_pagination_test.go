package asset

import (
	"context"
	"fmt"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// 231 locations, many sharing the same name across page boundaries, must page
// without duplicates or omissions and preserve ancestor paths (parent → child).
func TestLocationPaginationStableAndComplete(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	const n = 231
	// 5 sites; each site has children so ancestor paths exist.
	parentIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		loc, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: fmt.Sprintf("Site-%d", i)}, "actor", "req")
		if err != nil {
			t.Fatal(err)
		}
		parentIDs[i] = loc.Id
	}
	for i := 0; i < n; i++ {
		// Many duplicate names: reuse "Workshop" for most nodes to force the
		// secondary id sort to stabilize pagination.
		name := "Workshop"
		if i%7 == 0 {
			name = fmt.Sprintf("Unique-%d", i)
		}
		parent := parentIDs[i%5]
		if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", ParentId: &parent, Kind: "WORKSHOP", Name: name}, "actor", "req"); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	total := 0
	for offset := 0; ; offset += 100 {
		page, _, err := ListLocations(ctx, db, "Project-A", ListOptions{Limit: 100, Offset: offset, Sort: "name"})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, l := range page {
			if seen[l.Id] {
				t.Fatalf("duplicate id %q across pages", l.Id)
			}
			seen[l.Id] = true
			total++
		}
		if len(page) < 100 {
			break
		}
	}
	// 5 sites + 231 workshops = 236 nodes.
	if total != n+5 {
		t.Fatalf("paged locations = %d, want %d (no omission)", total, n+5)
	}
	// Ancestor path: a workshop's parent site must be present and reachable.
	page, _, _ := ListLocations(ctx, db, "Project-A", ListOptions{Limit: 1, Offset: 0})
	if len(page) == 0 {
		t.Fatal("no locations returned")
	}
	// Verify a child's parent resolves to one of the seeded sites.
	byID := map[string]contract.AssetLocation{}
	for offset := 0; ; offset += 100 {
		batch, _, err := ListLocations(ctx, db, "Project-A", ListOptions{Limit: 100, Offset: offset, Sort: "name"})
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, l := range batch {
			byID[l.Id] = l
		}
		if len(batch) < 100 {
			break
		}
	}
	for _, l := range byID {
		if l.ParentId != nil {
			parent, ok := byID[*l.ParentId]
			if !ok || parent.Kind != "SITE" {
				t.Fatalf("child %q parent %q missing or not a SITE", l.Id, *l.ParentId)
			}
		}
	}
}
