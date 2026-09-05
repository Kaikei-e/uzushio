package mine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Action is what a mining pass does to one pattern document.
type Action string

// The actions.
const (
	// ActionCreate writes a pattern the vault did not have.
	ActionCreate Action = "create"
	// ActionAppend adds run identifiers to an open pattern's evidence. A
	// pattern is a standing claim about the harness, and a second sighting is
	// more of the same claim rather than a new one — so the document grows
	// instead of multiplying.
	ActionAppend Action = "append"
	// ActionUnchanged is a pattern already citing every run the pass saw.
	ActionUnchanged Action = "unchanged"
	// ActionSkipClosed is a pattern that is no longer open. A resolved or
	// withdrawn pattern is a decision somebody made, and appending evidence to
	// it would reopen the decision silently. The runs are reported instead.
	ActionSkipClosed Action = "skip-closed"
	// ActionUnreadable is a document already at the path that this package
	// cannot read. One such file must not cost the pass its other patterns —
	// nor the proposals that follow them — so it is reported and stepped over.
	ActionUnreadable Action = "unreadable"
)

// ErrVault is the sentinel a malformed document already in the vault wraps. It
// is separate from ErrTrace because the two are different accusations: one says
// CMoA wrote something this build cannot read, the other says the vault holds
// something a person has to look at.
var ErrVault = errors.New("mine: unreadable vault document")

// Change is one planned write, or one refusal to write.
type Change struct {
	// ID is the pattern's identifier.
	ID string
	// Path is where it lives, relative to the vault root.
	Path string
	// Action is what the pass would do.
	Action Action
	// Status is the status the document on disk carries, empty for a create.
	Status string
	// Category and Component are what the document that will exist at Path
	// says about itself — the mined values for a create, and the ones already
	// on disk for everything else. A report built from the rule rather than
	// from the document would name a component the vault does not contain.
	Category  string
	Component string
	// Diverged names the fields on which the document on disk disagrees with
	// the rule that just fired. It is not an error: somebody may have
	// re-attributed a pattern by hand, and that judgement outranks the rule's.
	// It is worth saying out loud, because it means the loop will propose
	// against a surface the miner did not pick.
	Diverged []string
	// Err is why an unreadable document could not be read.
	Err error
	// Added are the run identifiers this pass contributes that the document
	// did not already cite, sorted.
	Added []string
	// Bytes is what would be written, nil where nothing would be.
	Bytes []byte
}

// Plan works out what a mining pass would do to a vault, without touching it.
// It is separate from Apply so that `--dry-run` reports exactly what a real run
// would write rather than an approximation of it.
//
// A document already in the vault that cannot be read is a Change of its own
// rather than a failure of the pass: thirteen good patterns and a whole round
// of proposals must not be lost to one file somebody hand-edited badly.
func Plan(vaultDir string, patterns []doc.Pattern) ([]Change, error) {
	changes := make([]Change, 0, len(patterns))
	for _, pattern := range patterns {
		change, err := planOne(vaultDir, pattern)
		if err != nil {
			if errors.Is(err, ErrVault) {
				changes = append(changes, Change{
					ID: pattern.PatternID, Action: ActionUnreadable, Err: err,
					Category: pattern.Category.String(), Component: pattern.Component,
				})
				continue
			}
			return nil, err
		}
		changes = append(changes, change)
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes, nil
}

func planOne(vaultDir string, pattern doc.Pattern) (Change, error) {
	relative, err := pattern.Path()
	if err != nil {
		return Change{}, err
	}
	change := Change{
		ID: pattern.PatternID, Path: relative,
		Category: pattern.Category.String(), Component: pattern.Component,
	}
	existing, found, err := ReadPattern(filepath.Join(vaultDir, relative))
	if err != nil {
		return Change{}, err
	}
	if !found {
		body, err := pattern.Bytes()
		if err != nil {
			return Change{}, err
		}
		change.Action, change.Added, change.Bytes = ActionCreate, pattern.Evidence, body
		return change, nil
	}
	change.Status = existing.Status.String()
	change.Category, change.Component = existing.Category.String(), existing.Component
	if existing.Category != pattern.Category {
		change.Diverged = append(change.Diverged, "category")
	}
	if existing.Component != pattern.Component {
		change.Diverged = append(change.Diverged, "component")
	}
	if existing.Status != vocab.StatusOpen {
		change.Action = ActionSkipClosed
		change.Added = missing(existing.Evidence, pattern.Evidence)
		return change, nil
	}
	added := missing(existing.Evidence, pattern.Evidence)
	if len(added) == 0 {
		change.Action = ActionUnchanged
		return change, nil
	}
	// Everything but the evidence is the document as it was written: the day it
	// was first seen, the context somebody may have sharpened by hand, the body
	// they may have added to. A mining pass adds sightings; it does not restate
	// the reading.
	grown := existing
	grown.Evidence = sortedUnique(append(append([]string(nil), existing.Evidence...), pattern.Evidence...))
	body, err := grown.Bytes()
	if err != nil {
		return Change{}, err
	}
	change.Action, change.Added, change.Bytes = ActionAppend, added, body
	return change, nil
}

// missing returns the values of want that are not already in have.
func missing(have, want []string) []string {
	present := map[string]bool{}
	for _, v := range have {
		present[v] = true
	}
	var out []string
	for _, v := range want {
		if !present[v] {
			out = append(out, v)
		}
	}
	return sortedUnique(out)
}

// Apply writes the plan. A change carrying no bytes writes nothing, so the
// caller can hand back the whole plan rather than filtering it.
func Apply(vaultDir string, changes []Change) error {
	for _, change := range changes {
		if len(change.Bytes) == 0 {
			continue
		}
		path := filepath.Join(vaultDir, change.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("mine: %w", err)
		}
		if err := writeFile(path, change.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes atomically: a document half on disk is a document DocDag
// reports as malformed, and a mining pass interrupted halfway should leave the
// vault as it was rather than leave a finding behind.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".uzushio-*")
	if err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mine: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	return nil
}

// ReadPattern reads one pattern document back. It answers false for a file that
// is not there, which is the ordinary case on the first mining pass.
//
// The reader is here rather than in internal/doc because internal/doc writes
// documents: reading one back is mining's own need, and the only thing it does
// with what it reads is grow the evidence list.
func ReadPattern(path string) (doc.Pattern, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return doc.Pattern{}, false, nil
	}
	if err != nil {
		return doc.Pattern{}, false, fmt.Errorf("mine: %w", err)
	}
	pattern, err := ParsePattern(raw)
	if err != nil {
		return doc.Pattern{}, false, fmt.Errorf("%w: %s: %w", ErrVault, path, err)
	}
	return pattern, true, nil
}

// ParsePattern reads a pattern document's frontmatter and body back into the
// value internal/doc writes.
func ParsePattern(raw []byte) (doc.Pattern, error) {
	front, title, body, err := split(raw)
	if err != nil {
		return doc.Pattern{}, err
	}
	var fm doc.PatternFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return doc.Pattern{}, fmt.Errorf("decode frontmatter: %w", err)
	}
	if fm.Kind != vocab.KindPattern.String() {
		return doc.Pattern{}, fmt.Errorf("kind is %q, not %s", fm.Kind, vocab.KindPattern)
	}
	if fm.Title == "" {
		fm.Title = title
	}
	return doc.Pattern{
		PatternID: fm.ID,
		Title:     fm.Title,
		Date:      fm.Date,
		Status:    vocab.Status(fm.Status),
		Category:  vocab.Category(fm.Category),
		Context:   fm.Context,
		Component: fm.Component,
		Evidence:  fm.Evidence,
		Body:      body,
	}, nil
}

// split takes a document apart into its frontmatter, its heading and the body
// under it, the way internal/doc's renderer put it together.
func split(raw []byte) (front []byte, title, body string, err error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", "", errors.New("no frontmatter: the document does not open with ---")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil, "", "", errors.New("no frontmatter: the opening --- is never closed")
	}
	front = []byte(rest[:end+1])
	after := strings.TrimLeft(rest[end+len("\n---\n"):], "\n")
	if strings.HasPrefix(after, "# ") {
		line, remainder, _ := strings.Cut(after, "\n")
		title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		after = remainder
	}
	return front, title, strings.Trim(after, "\n"), nil
}
