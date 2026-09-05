// Package doc writes the documents uzushio generates. An edit, a failure
// pattern, an evaluation run, a verifier health check and a judge calibration
// are written by the harness rather than by a person, and a machine writer that guesses at a
// frontmatter key is a corpus that fails validation on the day nobody is
// watching. So each kind is a Go struct whose fields are exactly the keys the
// generated docdag.yaml declares for it, Validate answers before anything is
// written, and Bytes produces the document DocDag reads back.
//
// All five kinds are closed, so a writer here emits only the keys the
// configuration declares, plus the engine's own — id, kind, title, date and
// status — and the edge keys. The vocabulary comes from internal/vocab and the
// surfaces from internal/surfaces, so there is one place a word is spelled.
package doc

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ErrDocument is the sentinel every validation failure wraps, so a caller can
// tell a malformed document from an I/O failure without reading message text.
var ErrDocument = errors.New("doc: invalid document")

// Document is what each writer answers with: an identity, the kind whose
// directory and vocabulary it answers to, the path it belongs at, the check it
// passes before it is written, and the bytes themselves.
type Document interface {
	ID() string
	Kind() vocab.Kind
	Path() (string, error)
	Validate() error
	Bytes() ([]byte, error)
}

// The five writers are Documents.
var (
	_ Document = Calibration{}
	_ Document = Edit{}
	_ Document = Pattern{}
	_ Document = Run{}
	_ Document = Verifier{}
)

// Prediction is one claim an edit makes about a failure pattern: the pattern,
// what the edit expects of it, and — written later, when a run has settled it —
// how the claim turned out.
type Prediction struct {
	Pattern string
	Expect  vocab.Expect
	Outcome vocab.Outcome
}

// Supersession is one edit replacing another, with the reason the spec preset
// requires of every supersedes entry.
type Supersession struct {
	Edit   string
	Reason string
}

// Validation is what one run measured about one edit: the model it ran on, the
// pass rate it reached and the pass rate the same suite gave without the edit.
// A rate without its baseline says nothing about a change, so both are written.
type Validation struct {
	Edit             string
	Model            string
	PassRate         float64
	BaselinePassRate float64
}

// predictsEntry, supersedesEntry and validatesEntry are the frontmatter forms
// of the three edges. DocDag reads an edge entry as a mapping whose `ref` names
// the far end and whose remaining keys are the edge's attributes.
type predictsEntry struct {
	Ref     string `yaml:"ref"`
	Expect  string `yaml:"expect"`
	Outcome string `yaml:"outcome,omitempty"`
}

type supersedesEntry struct {
	Ref    string `yaml:"ref"`
	Reason string `yaml:"reason"`
}

type validatesEntry struct {
	Ref              string  `yaml:"ref"`
	Model            string  `yaml:"model"`
	PassRate         float64 `yaml:"pass_rate"`
	BaselinePassRate float64 `yaml:"baseline_pass_rate"`
}

// render turns one frontmatter struct, a title and a body into the document
// DocDag reads:
//
//	---
//	<yaml>
//	---
//
//	# <title>
//
//	<body>
//
// The frontmatter is a struct rather than a map so the keys come out in
// declaration order on every run; goccy writes a struct's fields in the order
// they are declared, which is what makes a regenerated corpus byte-identical.
func render(frontmatter any, title, body string) ([]byte, error) {
	front, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal frontmatter: %w", ErrDocument, err)
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(front)
	out.WriteString("---\n\n# ")
	out.WriteString(title)
	out.WriteString("\n")
	if body = strings.TrimRight(body, "\n"); body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}
	// A raw tab or carriage return in a document is the one corruption that
	// travels silently: YAML forbids a tab as indentation, a CR turns a line
	// ending into something a diff reads as a change, and neither is visible in
	// a review. Refusing the bytes is cheaper than explaining them later.
	if i := bytes.IndexAny(out.Bytes(), "\t\r"); i >= 0 {
		return nil, fmt.Errorf("%w: rendered document holds a raw tab or carriage return at byte %d", ErrDocument, i)
	}
	return out.Bytes(), nil
}

// The identifier shapes of the three preset kinds an edit points at. They
// belong to the spec preset rather than to uzushio, which is why they are here
// rather than in internal/vocab: internal/vocab spells the words uzushio owns.
// The test that reads them back out of the assembled configuration is what
// keeps them in step with the preset.
const (
	// TopicIDPattern is a subject an edit is about.
	TopicIDPattern = `^topic/[a-z0-9/-]+$`
	// PremiseIDPattern is something an edit rests on being true.
	PremiseIDPattern = `^premise/[a-z0-9/-]+$`
	// PostMortemIDPattern is a published post-mortem an edit answers.
	PostMortemIDPattern = `^pm-\d{4}$`
)

var (
	topicID   = regexp.MustCompile(TopicIDPattern)
	premiseID = regexp.MustCompile(PremiseIDPattern)
	pmID      = regexp.MustCompile(PostMortemIDPattern)
)

// ValidTopicID reports whether id names a topic the way the spec preset spells
// one.
func ValidTopicID(id string) bool { return topicID.MatchString(id) }

// ValidPremiseID reports whether id names a premise.
func ValidPremiseID(id string) bool { return premiseID.MatchString(id) }

// ValidPostMortemID reports whether id names a post-mortem.
func ValidPostMortemID(id string) bool { return pmID.MatchString(id) }

// requireText reports a text field that has to carry something.
func requireText(what, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is empty", ErrDocument, what)
	}
	return nil
}

// requireDay reports a day that is not the one format DocDag reads a period
// and a date in.
func requireDay(what, value string) error {
	if _, err := time.Parse(vocab.DayLayout, value); err != nil {
		return fmt.Errorf("%w: %s %q is not a %s day", ErrDocument, what, value, vocab.DayLayout)
	}
	return nil
}

// optionalDay allows the key to be unwritten, and holds a written one to the
// same format.
func optionalDay(what, value string) error {
	if value == "" {
		return nil
	}
	return requireDay(what, value)
}

// requireVocabulary reports a value outside a closed vocabulary.
func requireVocabulary[T ~string](what string, value T, vocabulary []T) error {
	if !vocab.Valid(value, vocabulary) {
		return fmt.Errorf("%w: %s %q is outside %v", ErrDocument, what, string(value), vocab.Strings(vocabulary))
	}
	return nil
}

// requireSurface reports a component outside the harness surfaces CMoA
// declares. The list is read rather than written down: a surface that changes
// there changes what a document may name.
func requireSurface(what, name string) error {
	all, err := surfaces.All()
	if err != nil {
		return err
	}
	for _, surface := range all {
		if surface == name {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %q is not a harness surface (%v)", ErrDocument, what, name, all)
}

// supersedesEntries renders a list of supersessions as the frontmatter shape
// DocDag reads. It is here rather than in one kind's file because two kinds
// take part in the edge now: an edit replaces an edit, and a calibration
// replaces the last measurement of the same judge.
func supersedesEntries(all []Supersession) []supersedesEntry {
	if len(all) == 0 {
		return nil
	}
	out := make([]supersedesEntry, 0, len(all))
	for _, one := range all {
		out = append(out, supersedesEntry{Ref: one.Edit, Reason: one.Reason})
	}
	return out
}
