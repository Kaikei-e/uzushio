// Package surfaces holds the harness surfaces uzushio may edit, as CMoA
// declares them. The vocabulary is not uzushio's to invent: CMoA owns the
// harness, so the list is generated from `cmoa surfaces --format json` and
// committed, and everything downstream — the component field's vocabulary, the
// read-only rule, the propose-only rule, the human-approval rule — reads it
// from here.
package surfaces

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// The generator shells out because cmoa writes the vocabulary to stdout and
// has no output flag; the redirection is the whole of what sh is here for.
// It writes to a temporary name and renames only on success, so a missing or
// failing cmoa leaves the committed file as it was instead of truncating it.
//go:generate sh -c "cmoa surfaces --format json > cmoa-surfaces.json.tmp && mv cmoa-surfaces.json.tmp cmoa-surfaces.json"

// data is the committed output of `cmoa surfaces --format json`. Regenerate it
// with `go generate ./internal/surfaces` against a cmoa binary on PATH.
//
//go:embed cmoa-surfaces.json
var data []byte

// Count is the number of harness surfaces CMoA declares. It is asserted
// against the embedded file so a regeneration that changes the size of the
// vocabulary fails loudly here rather than silently widening a rule.
const Count = 7

// Autonomy words a surface answers to. They say who may accept an edit to the
// surface, not what the edit does.
const (
	AutonomyHumanApproval = "human-approval"
	AutonomyProposeOnly   = "propose-only"
	AutonomyAutoAccept    = "auto-accept"
)

// ErrInvalid is returned when the embedded vocabulary does not describe a
// usable set of surfaces.
var ErrInvalid = errors.New("surfaces: invalid vocabulary")

// Surface is one editable harness surface and the autonomy it is edited under.
type Surface struct {
	Name     string `json:"surface"`
	Autonomy string `json:"autonomy"`
}

// document is the shape `cmoa surfaces --format json` writes.
type document struct {
	Surfaces []Surface `json:"surfaces"`
	ReadOnly []string  `json:"read_only"`
}

// loaded is the parsed embedded document, decoded once.
var loaded = sync.OnceValues(func() (document, error) {
	return parse(data)
})

// Autonomies lists the autonomy words a surface may answer to, in the order
// they narrow: a person approves, a person is asked, nobody is asked.
func Autonomies() []string {
	return []string{AutonomyHumanApproval, AutonomyProposeOnly, AutonomyAutoAccept}
}

// parse decodes and validates one surfaces document.
func parse(raw []byte) (document, error) {
	var doc document
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return document{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := validate(doc); err != nil {
		return document{}, err
	}
	return doc, nil
}

// validate holds the vocabulary to what the rules downstream assume: seven
// uniquely named surfaces, every autonomy word known, and a read-only list
// that names none of them — a surface that is both editable and read-only
// would make edit_touches_readonly and the component vocabulary disagree.
func validate(doc document) error {
	if len(doc.Surfaces) != Count {
		return fmt.Errorf("%w: %d surfaces, want %d", ErrInvalid, len(doc.Surfaces), Count)
	}
	known := Autonomies()
	seen := make(map[string]bool, len(doc.Surfaces))
	for _, s := range doc.Surfaces {
		if s.Name == "" {
			return fmt.Errorf("%w: a surface has no name", ErrInvalid)
		}
		if seen[s.Name] {
			return fmt.Errorf("%w: surface %q is declared twice", ErrInvalid, s.Name)
		}
		seen[s.Name] = true
		if !slices.Contains(known, s.Autonomy) {
			return fmt.Errorf("%w: surface %q: unknown autonomy %q", ErrInvalid, s.Name, s.Autonomy)
		}
	}
	readOnly := make(map[string]bool, len(doc.ReadOnly))
	for _, name := range doc.ReadOnly {
		if name == "" {
			return fmt.Errorf("%w: a read-only component has no name", ErrInvalid)
		}
		if readOnly[name] {
			return fmt.Errorf("%w: read-only component %q is listed twice", ErrInvalid, name)
		}
		readOnly[name] = true
		if seen[name] {
			return fmt.Errorf("%w: %q is both an editable surface and read-only", ErrInvalid, name)
		}
	}
	return nil
}

// All returns every editable surface name, in the order CMoA declares them.
// The order is the vocabulary's own and is what the generated configuration
// writes, so a regeneration that only reorders the list is a visible diff.
func All() ([]string, error) {
	doc, err := loaded()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Surfaces))
	for _, s := range doc.Surfaces {
		names = append(names, s.Name)
	}
	return names, nil
}

// ByAutonomy returns the surfaces edited under one autonomy word, in
// declaration order. An unknown word is an error rather than an empty list: a
// rule built over the empty list would fire nowhere and say nothing.
func ByAutonomy(autonomy string) ([]string, error) {
	doc, err := loaded()
	if err != nil {
		return nil, err
	}
	if !slices.Contains(Autonomies(), autonomy) {
		return nil, fmt.Errorf("%w: unknown autonomy %q", ErrInvalid, autonomy)
	}
	names := []string{}
	for _, s := range doc.Surfaces {
		if s.Autonomy == autonomy {
			names = append(names, s.Name)
		}
	}
	return names, nil
}

// ReadOnly returns the harness components no edit may touch.
func ReadOnly() ([]string, error) {
	doc, err := loaded()
	if err != nil {
		return nil, err
	}
	return slices.Clone(doc.ReadOnly), nil
}

// HumanApproval returns the surfaces whose edits a person accepts.
func HumanApproval() ([]string, error) { return ByAutonomy(AutonomyHumanApproval) }

// ProposeOnly returns the surfaces whose edits are proposed and never accepted.
func ProposeOnly() ([]string, error) { return ByAutonomy(AutonomyProposeOnly) }

// Embedded returns the committed bytes, so a check can compare them against a
// live cmoa without reaching into the package's variables.
func Embedded() []byte { return slices.Clone(data) }
