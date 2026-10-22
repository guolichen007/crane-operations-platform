package boot

import (
	"encoding/json"
	"errors"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/platform/atomicfile"
	"os"
)

type Identity struct {
	ID  string
	Seq int64
}

// Start is called once per actual Agent startup, never on an MQTT reconnect.
// The configured device directory has a single Agent writer.
func Start(path string) (Identity, error) {
	var state struct {
		BootSeq int64 `json:"bootSeq"`
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Identity{}, err
	}
	if err == nil {
		var stored map[string]json.RawMessage
		if json.Unmarshal(b, &stored) != nil || stored["bootSeq"] == nil {
			return Identity{}, errors.New("missing durable boot counter")
		}
		if err = json.Unmarshal(b, &state); err != nil {
			return Identity{}, err
		}
		if state.BootSeq < 1 {
			return Identity{}, errors.New("invalid durable boot counter")
		}
	}
	if state.BootSeq < 0 || state.BootSeq >= 9007199254740991 {
		return Identity{}, errors.New("invalid or exhausted boot counter")
	}
	state.BootSeq++
	b, err = json.Marshal(state)
	if err != nil {
		return Identity{}, err
	}
	if err = atomicfile.Write(path, b); err != nil {
		return Identity{}, err
	}
	return Identity{ID: platform.ID(), Seq: state.BootSeq}, nil
}
