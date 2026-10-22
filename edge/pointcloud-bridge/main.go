// Phase 0 establishes only the adapter and binary contract. No ROS or upload.
package main

import (
	"flag"
	"fmt"
)

func main() {
	source := flag.String("source-topic", "", "configured ROS topic, never a core constant")
	flag.Parse()
	fmt.Printf("PointCloud bridge disabled until Phase 2; source configured=%t\n", *source != "")
}
