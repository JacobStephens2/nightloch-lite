// Command nightloch is the Nightloch Lite control plane.
// Slice 0: binary builds; HTTP and enforcement land in later slices.
package main

import (
	"fmt"
	"os"
)

// version is overridden at link time in CI when desired.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	fmt.Fprintf(os.Stderr, "nightloch %s — control plane not yet listening (slice 0 skeleton)\n", version)
	fmt.Fprintf(os.Stderr, "See plan.md; next: slice 1 (origin) / slice 2 (auth).\n")
	os.Exit(0)
}
