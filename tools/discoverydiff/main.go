// Command discoverydiff compares committed discovery pins to documents the
// caller already fetched. It does not open a network connection.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/TECMANIC/gplay/internal/discoverypin"
)

func main() {
	pinsPath := flag.String("pins", "", "path to discovery/pins.json")
	publisher := flag.String("publisher", "", "path to the androidpublisher discovery document")
	reporting := flag.String("reporting", "", "path to the playdeveloperreporting discovery document")
	gomod := flag.String("gomod", "", "path to go.mod")
	flag.Parse()
	if *pinsPath == "" || *publisher == "" || *reporting == "" || *gomod == "" {
		fmt.Fprintln(os.Stderr, "discoverydiff: -pins, -publisher, -reporting, and -gomod are required")
		os.Exit(2)
	}
	if err := run(*pinsPath, *publisher, *reporting, *gomod); err != nil {
		fmt.Fprintf(os.Stderr, "discoverydiff: %s\n", err)
		os.Exit(1)
	}
}

func run(pinsPath, publisher, reporting, gomod string) error {
	// Paths are the documents the workflow or the operator named. There is no
	// other input channel for this comparison tool.
	pinsRaw, err := os.ReadFile(pinsPath) // #nosec G304 -- operator-supplied pin file
	if err != nil {
		return err
	}
	pins, err := discoverypin.ParsePins(bytes.NewReader(pinsRaw))
	if err != nil {
		return err
	}
	pub, err := os.ReadFile(publisher) // #nosec G304 -- operator-supplied discovery document
	if err != nil {
		return err
	}
	rep, err := os.ReadFile(reporting) // #nosec G304 -- operator-supplied discovery document
	if err != nil {
		return err
	}
	mod, err := os.ReadFile(gomod) // #nosec G304 -- operator-supplied go.mod
	if err != nil {
		return err
	}
	return discoverypin.Check(pins, pub, rep, mod)
}
