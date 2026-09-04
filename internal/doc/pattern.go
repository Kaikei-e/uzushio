package doc

import (
	"fmt"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Pattern is a recurring failure written as an STPA unsafe control action: the
// component whose control went wrong, the way it went wrong, and the context it
// went wrong in. The context is not decoration — an unsafe control action that
// names no context is a component and a verb, and says nothing about when the
// harness is in trouble.
//
// The identifier carries a slash, so a pattern always writes `id:`: DocDag
// reads an identity off the file name's stem otherwise, and a stem cannot hold
// a slash.
type Pattern struct {
	// PatternID is the identifier, as `fp/retry-storm`.
	PatternID string
	// Title is the heading and the frontmatter title.
	Title string
	// Date is the day the pattern was first written down.
	Date string
	// Status is open until an edit has fixed it.
	Status vocab.Status
	// Category is which of the four unsafe control action types this is.
	Category vocab.Category
	// Context is the situation the control action is unsafe in.
	Context string
	// Component is the harness surface the pattern is about.
	Component string
	// Evidence names the CMoA trace runs the pattern was seen in — CMoA's own
	// run identifiers, not uzushio's. A pattern is a reading of what the
	// harness did, and what the harness did is in CMoA's traces; the evaluation
	// runs uzushio writes are reached the other way round, from the edit that
	// answers the pattern, across the validates edge.
	Evidence []string
	// Body is the Markdown under the heading.
	Body string
}

// PatternFrontmatter is a pattern's frontmatter in the order it is written.
type PatternFrontmatter struct {
	ID        string   `yaml:"id"`
	Kind      string   `yaml:"kind"`
	Title     string   `yaml:"title"`
	Status    string   `yaml:"status"`
	Date      string   `yaml:"date"`
	Category  string   `yaml:"category"`
	Context   string   `yaml:"context"`
	Component string   `yaml:"component"`
	Evidence  []string `yaml:"evidence,omitempty"`
}

// ID returns the pattern's identifier.
func (p Pattern) ID() string { return p.PatternID }

// Kind returns the kind a pattern answers to.
func (p Pattern) Kind() vocab.Kind { return vocab.KindPattern }

// Path returns where the pattern is written, relative to the vault root.
func (p Pattern) Path() (string, error) { return vocab.Path(vocab.KindPattern, p.PatternID) }

// Validate reports the first thing about the pattern DocDag would refuse.
func (p Pattern) Validate() error {
	if !vocab.ValidPatternID(p.PatternID) {
		return fmt.Errorf("%w: %q is not a pattern identifier (want %s)",
			ErrDocument, p.PatternID, vocab.PatternIDPattern)
	}
	if err := requireText("pattern "+p.PatternID+" title", p.Title); err != nil {
		return err
	}
	if err := requireDay("pattern "+p.PatternID+" date", p.Date); err != nil {
		return err
	}
	if err := requireVocabulary("pattern "+p.PatternID+" status", p.Status, vocab.PatternStatuses()); err != nil {
		return err
	}
	if err := requireVocabulary("pattern "+p.PatternID+" category", p.Category, vocab.AllCategories()); err != nil {
		return err
	}
	if err := requireText("pattern "+p.PatternID+" context", p.Context); err != nil {
		return err
	}
	if err := requireSurface("pattern "+p.PatternID+" component", p.Component); err != nil {
		return err
	}
	return validateIDs("pattern "+p.PatternID+" evidence", p.Evidence,
		vocab.ValidCMoATraceID, vocab.CMoATraceIDPattern)
}

// Frontmatter returns the pattern's frontmatter.
func (p Pattern) Frontmatter() (PatternFrontmatter, error) {
	if err := p.Validate(); err != nil {
		return PatternFrontmatter{}, err
	}
	return PatternFrontmatter{
		ID:        p.PatternID,
		Kind:      vocab.KindPattern.String(),
		Title:     p.Title,
		Status:    p.Status.String(),
		Date:      p.Date,
		Category:  p.Category.String(),
		Context:   p.Context,
		Component: p.Component,
		Evidence:  p.Evidence,
	}, nil
}

// Bytes returns the document as it is written to disk.
func (p Pattern) Bytes() ([]byte, error) {
	front, err := p.Frontmatter()
	if err != nil {
		return nil, err
	}
	return render(front, p.Title, p.Body)
}
