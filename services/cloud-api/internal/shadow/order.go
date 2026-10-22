package shadow

import contract "github.com/guolichen007/crane-operations-platform/contracts/go"

// Accept separates ordering within one boot from device restarts. Only a newer birth
// may establish a different boot. An old delayed boot cannot overwrite a new one.
func Accept(old, next contract.Message) bool {
	if old.BootId == "" {
		return next.Kind == "BIRTH" || next.Kind == "SNAPSHOT"
	}
	if old.BootId != next.BootId {
		return next.Kind == "BIRTH" && next.Timestamp.After(old.Timestamp)
	}
	if next.Kind == "LWT" {
		return old.Connectivity != "OFFLINE"
	}
	return next.Sequence > old.Sequence
}
