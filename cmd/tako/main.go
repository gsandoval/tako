// Command tako is the Tako CLI: dev, deploy, gen, test, logs, destroy,
// prune.
package main

import (
	"fmt"
	"os"
)

const usage = `tako — a Go framework over Google Cloud Run

Usage:
  tako <command>

Commands (arriving per the implementation plan):
  dev        run the app locally with watch and reload
  gen        print the AppGraph and generated sources
  test       run the app's tests with fresh generation
  deploy     analyze, build, provision, and ship to GCP
  logs       tail a deployed service's logs
  destroy    tear down everything tako provisioned
  prune      remove orphaned stateless resources

No commands are implemented yet (S0.1: repo skeleton and SDK stubs).
`

func main() {
	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
}
