// Package discoverypin compares committed Play discovery revisions and the
// google.golang.org/api module version. It does not fetch anything.
package discoverypin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const googleAPIModule = "google.golang.org/api"

// APIPin is one discovery document's identity.
type APIPin struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

// Pins is the committed discovery/pins.json document.
type Pins struct {
	AndroidPublisher       APIPin `json:"androidpublisher"`
	PlayDeveloperReporting APIPin `json:"playdeveloperreporting"`
	GoogleAPIModule        string `json:"google_api_module"`
}

// ParsePins decodes the pin file. Unknown keys are rejected.
func ParsePins(r io.Reader) (Pins, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var pins Pins
	if err := dec.Decode(&pins); err != nil {
		return Pins{}, fmt.Errorf("parse pins: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Pins{}, errors.New("parse pins: extra JSON after the object")
	}
	if err := validatePin("androidpublisher", pins.AndroidPublisher); err != nil {
		return Pins{}, err
	}
	if err := validatePin("playdeveloperreporting", pins.PlayDeveloperReporting); err != nil {
		return Pins{}, err
	}
	if pins.GoogleAPIModule == "" {
		return Pins{}, errors.New("google_api_module is empty")
	}
	return pins, nil
}

func validatePin(name string, pin APIPin) error {
	if pin.Version == "" || pin.Revision == "" {
		return fmt.Errorf("%s pin needs version and revision", name)
	}
	return nil
}

// Check reports every mismatch between pins, the two discovery documents, and go.mod.
// Discovery documents are compared on version and revision only.
func Check(pins Pins, publisherDoc, reportingDoc, goMod []byte) error {
	var errs []error
	if err := match("androidpublisher", pins.AndroidPublisher, publisherDoc); err != nil {
		errs = append(errs, err)
	}
	if err := match("playdeveloperreporting", pins.PlayDeveloperReporting, reportingDoc); err != nil {
		errs = append(errs, err)
	}
	got, err := ModuleVersion(goMod, googleAPIModule)
	if err != nil {
		errs = append(errs, err)
	} else if got != pins.GoogleAPIModule {
		errs = append(errs, fmt.Errorf("%s is %s, pin is %s", googleAPIModule, got, pins.GoogleAPIModule))
	}
	return errors.Join(errs...)
}

type identity struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

func match(name string, pin APIPin, doc []byte) error {
	if err := validatePin(name, pin); err != nil {
		return err
	}
	var id identity
	if err := json.Unmarshal(doc, &id); err != nil {
		return fmt.Errorf("%s discovery document: %w", name, err)
	}
	if id.Version == "" || id.Revision == "" {
		return fmt.Errorf("%s discovery document is missing version or revision", name)
	}
	if id.Version != pin.Version || id.Revision != pin.Revision {
		return fmt.Errorf("%s discovery is %s revision %s, pin is %s revision %s",
			name, id.Version, id.Revision, pin.Version, pin.Revision)
	}
	return nil
}

// ModuleVersion returns the version required for module in a go.mod file.
func ModuleVersion(goMod []byte, module string) (string, error) {
	var found string
	for line := range bytes.SplitSeq(goMod, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		fields := strings.Fields(text)
		ver := ""
		for i, field := range fields {
			if field == module && i+1 < len(fields) {
				ver = fields[i+1]
				break
			}
		}
		if ver == "" || strings.HasPrefix(ver, "//") {
			continue
		}
		if found != "" && found != ver {
			return "", fmt.Errorf("%s appears as both %s and %s", module, found, ver)
		}
		found = ver
	}
	if found == "" {
		return "", fmt.Errorf("%s is not required by go.mod", module)
	}
	return found, nil
}
