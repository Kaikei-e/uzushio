// Package fixture builds the lint fixtures uzushio's own rules answer to.
//
// DocDag asks every configured rule for a pair of corpora: one under ruleid/
// where the rule fires, one under ok/ where it stays silent. The pair is what
// turns a rule from a claim into something that has been shown to work — and it
// is what stops a rule that fires nowhere in a young vault from being read as a
// rule that cannot fire at all.
//
// The corpora are built from internal/doc rather than written by hand, so a
// frontmatter key that changes shape changes every fixture at once, and a
// fixture that stopped being a valid document fails at the point it is built.
// The layout is DocDag's: lint/<name>/{ruleid,ok}/<kind dir>/<file>.md.
//
// lint/ also holds fixtures copied from DocDag for the preset's own rules; they
// are not this package's to write, and Write never touches a directory Names
// does not list. See lint/README.md.
package fixture

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ErrFixture is the sentinel every failure to build or compare a corpus wraps.
var ErrFixture = errors.New("fixture: invalid corpus")

// The two sides of every fixture, named as DocDag names them.
const (
	// SideFires is the corpus where the rule has to report something.
	SideFires = "ruleid"
	// SideSilent is the corpus where it must not.
	SideSilent = "ok"
)

// written is the part of a document a corpus needs: where it goes and what it
// says. internal/doc's three writers satisfy it, and so does the one preset
// document a fixture cannot do without.
type written interface {
	Path() (string, error)
	Bytes() ([]byte, error)
}

// corpus is one fixture name and the two document sets that answer for it.
type corpus struct {
	name   string
	fires  []written
	silent []written
}

// side returns the documents on one side of a corpus.
func (c corpus) side(name string) []written {
	if name == SideFires {
		return c.fires
	}
	return c.silent
}

// Names lists the fixtures this package writes, in the order it declares them.
// A directory under lint/ that is not named here was put there by hand or
// copied from DocDag, and Write leaves it alone.
func Names() ([]string, error) {
	all, err := corpora()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.name)
	}
	return names, nil
}

// files returns every file the fixtures amount to, keyed by its path relative
// to the fixtures directory. Building them all up front is what lets Write and
// Check answer from one description rather than two.
func files() (map[string][]byte, error) {
	out := map[string][]byte{}
	seen := map[string]bool{}
	all, err := corpora()
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if seen[c.name] {
			return nil, fmt.Errorf("%w: fixture %q is declared twice", ErrFixture, c.name)
		}
		seen[c.name] = true
		for _, side := range []string{SideFires, SideSilent} {
			documents := c.side(side)
			if len(documents) == 0 {
				return nil, fmt.Errorf("%w: fixture %q has no %s document", ErrFixture, c.name, side)
			}
			for _, document := range documents {
				relative, err := document.Path()
				if err != nil {
					return nil, fmt.Errorf("%w: fixture %q %s: %w", ErrFixture, c.name, side, err)
				}
				body, err := document.Bytes()
				if err != nil {
					return nil, fmt.Errorf("%w: fixture %q %s: %w", ErrFixture, c.name, side, err)
				}
				key := path.Join(c.name, side, relative)
				if _, taken := out[key]; taken {
					return nil, fmt.Errorf("%w: fixture %q %s writes %s twice", ErrFixture, c.name, side, relative)
				}
				out[key] = body
			}
		}
	}
	return out, nil
}

// Write materialises the fixtures under dir, one directory per name. Each name's
// directory is removed first, so a document that stopped being generated stops
// existing rather than lingering as a corpus nobody meant to keep.
func Write(dir string) error {
	all, err := files()
	if err != nil {
		return err
	}
	names, err := Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("fixture: clear %s: %w", filepath.Join(dir, name), err)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(all)) {
		target := filepath.Join(dir, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("fixture: create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, all[key], 0o644); err != nil {
			return fmt.Errorf("fixture: write %s: %w", target, err)
		}
	}
	return nil
}

// Check compares what is on disk under dir against what Write would put there
// and returns the paths that differ, relative to dir and sorted: a file whose
// bytes disagree, one that is missing, and one that is there and should not be.
// An empty result is a generated tree nobody has edited by hand.
func Check(dir string) ([]string, error) {
	want, err := files()
	if err != nil {
		return nil, err
	}
	got, err := onDisk(dir)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for key, body := range want {
		if !bytes.Equal(got[key], body) {
			changed = append(changed, key)
		}
	}
	for key := range got {
		if _, generated := want[key]; !generated {
			changed = append(changed, key)
		}
	}
	slices.Sort(changed)
	return changed, nil
}

// onDisk reads the files under the generated fixture directories. A name with
// no directory at all reads as no files, which Check reports as every one of
// its files having changed.
func onDisk(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	names, err := Names()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		root := filepath.Join(dir, name)
		err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case entry.IsDir():
				return nil
			}
			relative, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(relative)] = body
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("fixture: read %s: %w", root, err)
		}
	}
	return out, nil
}

// raw is a document written as text rather than through internal/doc, because
// internal/doc refuses to write it. Two of the edit_touches_readonly fixtures
// are exactly that: an edit that states no blast radius, and an edit that names
// a read-only component. Both are documents the vault reports as errors, which
// is the whole point of the corpus they sit in — and a writer that could
// produce them would be a writer the harness could produce them with.
type raw struct {
	// relative is the document's path under the corpus root, as the kind's
	// directory and file name give it.
	relative string
	// text is the document, byte for byte.
	text string
}

// Path returns where the document is written, relative to the corpus root.
func (r raw) Path() (string, error) {
	if r.relative == "" {
		return "", fmt.Errorf("%w: a raw fixture document names no path", ErrFixture)
	}
	return r.relative, nil
}

// Bytes returns the document as it is written to disk.
func (r raw) Bytes() ([]byte, error) {
	if r.text == "" {
		return nil, fmt.Errorf("%w: raw fixture document %s is empty", ErrFixture, r.relative)
	}
	return []byte(r.text), nil
}

// topic is the one document a fixture needs that uzushio does not write: the
// subject an edit's about: edge points at. The topic kind belongs to the spec
// preset rather than to uzushio, so it is written here as text rather than
// given a writer of its own in internal/doc.
type topic struct {
	slug  string
	title string
	body  string
}

// Path returns where the topic is written, relative to the corpus root.
func (t topic) Path() (string, error) {
	if t.slug == "" || strings.ContainsAny(t.slug, "/ \t") {
		return "", fmt.Errorf("%w: topic slug %q is not a file name", ErrFixture, t.slug)
	}
	return path.Join("spec/topics", t.slug+".md"), nil
}

// Bytes returns the document as it is written to disk.
func (t topic) Bytes() ([]byte, error) {
	if _, err := t.Path(); err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, "---\nid: topic/%s\nkind: topic\ntitle: %s\ndate: %q\n---\n\n# %s\n\n%s\n",
		t.slug, t.title, fixtureDay, t.title, t.body), nil
}
