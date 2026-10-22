package boot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentStartupAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot.json")
	first, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Start(path)
	if err != nil || second.Seq != first.Seq+1 || second.ID == first.ID {
		t.Fatal("boot was not durable", second, err)
	}
	for _, bad := range []string{`{"bootSeq":-1}`, `{"bootSeq":0}`, `{}`, ``, `{"bootSeq":null}`} {
		if err = os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Start(path); err == nil {
			t.Fatal("corrupt counter reset silently", bad)
		}
	}
}
