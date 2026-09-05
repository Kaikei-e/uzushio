package proposeedit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// editFile is the name an edit document is written under, which is also where
// its number is read from.
var editFile = regexp.MustCompile(`^he-(\d{4})\.md$`)

// Write is one edit as it would land in a vault: the document, and the sidecar
// diff where the component keeps its content in one.
type Write struct {
	// ID is the identifier assigned to the edit.
	ID string
	// Path is the document, relative to the vault root.
	Path string
	// Bytes is the document.
	Bytes []byte
	// DiffPath is the sidecar diff, relative to the vault root, empty where
	// there is none.
	DiffPath string
	// DiffBytes is the sidecar diff.
	DiffBytes []byte
	// Component and Approval are carried for the report: they are what decide
	// whether a person has to look at this one.
	Component string
	Approval  string
}

// Numbering hands out edit identifiers within one process.
//
// It exists because the vault is not a reliable counter mid-pass. `Plan` is
// called once per pattern, and under `--dry-run` nothing is written between
// calls — so a plan that rescanned the directory each time would assign
// he-0001 to every pattern and report identifiers that are not the ones a real
// run would use. The scan happens once, at the highest number on disk; the
// counter advances across every Plan in the process; and Apply still opens each
// file with O_EXCL, so a number another process took in the meantime is refused
// rather than overwritten.
type Numbering struct {
	next int
}

// NewNumbering reads the vault's highest edit number and starts after it.
func NewNumbering(vaultDir string) (*Numbering, error) {
	next, err := NextEditNumber(vaultDir)
	if err != nil {
		return nil, err
	}
	return &Numbering{next: next}, nil
}

// take returns the next free identifier.
func (n *Numbering) take() (string, error) {
	id, err := vocab.EditID(n.next)
	if err != nil {
		return "", fmt.Errorf("%w: the vault has used every four-digit edit number up to he-%04d; "+
			"the identifier shape has to grow before another edit can be written", ErrPropose, vocab.EditNumberMax)
	}
	n.next++
	return id, nil
}

// Plan assigns identifiers and renders the documents, without writing anything.
// Numbers are assigned in the order the proposals came, which is the order of
// the proposers, so two passes over the same run give the same numbering.
func Plan(numbering *Numbering, proposals []Proposal) ([]Write, error) {
	writes := make([]Write, 0, len(proposals))
	for _, proposal := range proposals {
		id, err := numbering.take()
		if err != nil {
			return nil, err
		}
		edit := proposal.Edit
		edit.EditID = id
		// The check is repeated here on purpose. Convert ran it against a
		// document with a placeholder identifier; this is the document that
		// will be on disk, and an edit `run` refuses is worse than one that was
		// never written.
		if err := edit.CheckPaths(); err != nil {
			return nil, err
		}
		body, err := edit.Bytes()
		if err != nil {
			return nil, err
		}
		relative, err := edit.Path()
		if err != nil {
			return nil, err
		}
		write := Write{
			ID:        id,
			Path:      relative,
			Bytes:     body,
			Component: edit.Component,
			Approval:  edit.Approval.String(),
		}
		if len(proposal.Diff) > 0 {
			diffPath, err := doc.DiffPath(id)
			if err != nil {
				return nil, err
			}
			write.DiffPath, write.DiffBytes = diffPath, proposal.Diff
		}
		writes = append(writes, write)
	}
	return writes, nil
}

// Apply writes the plan.
func Apply(vaultDir string, writes []Write) error {
	for _, write := range writes {
		if err := writeUnder(vaultDir, write.Path, write.Bytes); err != nil {
			return err
		}
		if write.DiffPath != "" {
			if err := writeUnder(vaultDir, write.DiffPath, write.DiffBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeUnder writes one file under the vault, refusing to overwrite: an
// identifier assigned from the free numbers should never land on a document
// that is already there, and if it does, the numbering is wrong and silence
// would lose somebody's edit.
func writeUnder(vaultDir, relative string, body []byte) error {
	full := filepath.Join(vaultDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrPropose, err)
	}
	file, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPropose, err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: %w", ErrPropose, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: %w", ErrPropose, err)
	}
	return nil
}

// NextEditNumber returns the first edit number the vault has not used. It reads
// the directory rather than a counter: a counter is a second source of truth
// about what is in the vault, and the vault is the first one.
//
// It reads only the four-digit shape the identifier declares. A file outside it
// is not an edit whatever it is named, and Apply's O_EXCL is what catches the
// case where that assumption was wrong.
func NextEditNumber(vaultDir string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(vaultDir, vocab.DirEdits))
	if os.IsNotExist(err) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrPropose, err)
	}
	highest := 0
	for _, entry := range entries {
		match := editFile.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		number, err := vocab.ParseEditID("he-" + match[1])
		if err != nil {
			return 0, err
		}
		if number > highest {
			highest = number
		}
	}
	return highest + 1, nil
}

// TopicPath returns where a topic document lives, relative to the vault root.
// The topic kind is the spec preset's rather than uzushio's, which is why the
// directory is spelled here and not in internal/vocab.
func TopicPath(topicID string) (string, error) {
	if !doc.ValidTopicID(topicID) {
		return "", fmt.Errorf("%w: %q is not a topic identifier (want %s)",
			ErrPropose, topicID, doc.TopicIDPattern)
	}
	return path.Join("spec/topics", path.Base(topicID)+".md"), nil
}

// singleSegmentTopic reports whether a topic identifier has one segment after
// `topic/`. DocDag names a document after the last segment of its identifier,
// so `topic/a/x` and `topic/b/x` are two identifiers and one file — which is
// fine to read and not fine to create.
func singleSegmentTopic(topicID string) bool {
	return !strings.Contains(strings.TrimPrefix(topicID, "topic/"), "/")
}

// TopicExists reports whether the vault already carries a topic. An edit in
// force has to name a topic that is there, so `improve` asks before it writes
// rather than leaving the vault with a dangling reference.
func TopicExists(vaultDir, topicID string) (bool, error) {
	relative, err := TopicPath(topicID)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(vaultDir, filepath.FromSlash(relative)))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrPropose, err)
	}
	return true, nil
}

// TopicDocument renders the topic a proposed harness edit is about, for a vault
// that has no subject for one yet. It is the preset's shape and nothing more:
// four keys and a paragraph saying what the subject is, so that two edits about
// the harness can be seen to be about the same thing.
func TopicDocument(topicID, title, day, body string) []byte {
	var out []byte
	out = append(out, "---\nid: "...)
	out = append(out, topicID...)
	out = append(out, "\nkind: topic\ntitle: "...)
	out = append(out, title...)
	out = append(out, "\ndate: "...)
	out = append(out, day...)
	out = append(out, "\n---\n\n# "...)
	out = append(out, title...)
	out = append(out, "\n\n"...)
	out = append(out, body...)
	out = append(out, '\n')
	return out
}

// DefaultTopicTitle and DefaultTopicBody are what DefaultTopic says about
// itself where `improve` has to write it.
const (
	DefaultTopicTitle = "Changing the harness the agent is given"
	DefaultTopicBody  = "The files an agent is handed before it sees a task — the system prompt\n" +
		"appended to its contract, the notes it always reads, the skills it is told\n" +
		"it has. This is the subject of every edit that changes what the agent is\n" +
		"told rather than what it is asked, and of every measurement of whether\n" +
		"telling it that made it better at anything."
)

// WriteTopic writes a topic document if the vault has none. It answers whether
// it wrote one.
func WriteTopic(vaultDir, topicID, day string) (bool, error) {
	exists, err := TopicExists(vaultDir, topicID)
	if err != nil || exists {
		return false, err
	}
	relative, err := TopicPath(topicID)
	if err != nil {
		return false, err
	}
	if !singleSegmentTopic(topicID) {
		return false, fmt.Errorf(
			"%w: %s is written to %s, which another topic ending in the same segment would also claim; "+
				"write it by hand or name a single-segment topic", ErrPropose, topicID, relative)
	}
	title, body := DefaultTopicTitle, DefaultTopicBody
	if topicID != DefaultTopic {
		title = "The subject " + topicID + " names"
		body = "Written by `uzushio improve` so that a proposed harness edit has a subject\n" +
			"to be about. Replace this paragraph with what the subject actually is."
	}
	if err := writeUnder(vaultDir, relative, TopicDocument(topicID, title, day, body)); err != nil {
		return false, err
	}
	return true, nil
}
