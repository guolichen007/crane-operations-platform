package migrations

import (
	"embed"
	"io/fs"
	"strconv"
)

//go:embed *.sql
var Files embed.FS

// Latest is the highest migration version shipped in this source (the schema version every
// freshly migrated database has). Tests compare against it instead of a literal, so adding a
// forward migration does not silently keep asserting an old version.
func Latest() int64 {
	entries, err := fs.Glob(Files, "*.sql")
	if err != nil {
		panic(err)
	}
	var latest int64
	for _, name := range entries {
		if len(name) < 6 || name[5] != '_' {
			continue
		}
		if v, err := strconv.ParseInt(name[:5], 10, 64); err == nil && v > latest {
			latest = v
		}
	}
	return latest
}
