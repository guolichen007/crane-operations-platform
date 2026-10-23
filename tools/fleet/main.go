package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"os"
)

func main() {
	path := flag.String("fleet-file", "", "manifest")
	inv := flag.String("inventory-file", "", "private inventory")
	count := flag.Int("count", 20, "legacy fixture count")
	out := flag.String("out", "deploy/compose/runtime/acl", "development ACL output")
	manifestOut := flag.String("manifest-out", "", "optional generated fleet path")
	flag.Parse()
	m, err := fleet.Load(*path, *count, true, false)
	if *inv != "" {
		d, e := inventory.Load(*inv)
		err = e
		if err == nil {
			m = inventory.Fleet(d)
		}
	}
	if err == nil {
		var acl string
		acl, err = fleet.ACL(m)
		if err == nil {
			err = os.WriteFile(*out, []byte(acl), 0600)
		}
	}
	if err == nil && *manifestOut != "" {
		b, e := json.MarshalIndent(m, "", "  ")
		err = e
		if err == nil {
			err = os.WriteFile(*manifestOut, b, 0600)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fleet generation failed; check manifest without publishing its contents.")
		os.Exit(1)
	}
}
