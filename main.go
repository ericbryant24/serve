// serve shows documents in the browser and lets people review them with
// inline comments that an agent reads, answers and resolves from the command
// line. See README.md.
package main

import (
	"os"

	"serve/internal/cli"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Main(version, os.Args[1:]))
}
