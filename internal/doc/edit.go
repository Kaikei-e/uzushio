package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/Kaikei-e/DocDag/config"

	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Edit is a proposed change to one harness surface: what it changes, what it
// claims, and — once runs have settled the claim — whether it was accepted and
// by whom.
//
// The identifier is file-name shaped, so an edit writes no `id:` key: DocDag
// reads the identity off the file name's stem. Every other key the struct
// carries is one the edit kind declares.
type Edit struct {
	// EditID is the identifier, as `he-0001`.
	EditID string
	// Title is the heading and the frontmatter title, one sentence saying what
	// the edit does.
	Title string
	// Date is the day the edit was written, as YYYY-MM-DD.
	Date string
	// Status is where the edit stands in its lifecycle.
	Status vocab.Status
	// Component is the one harness surface the edit is about.
	Component string
	// Touches lists every surface the edit changes. It is required even where
	// there is only one: the edit_touches_readonly rule reads it as the edit's
	// blast radius, and an unwritten list reads as a radius nobody stated,
	// which the vault reports as an error. Touching sets it to the component
	// where there is nothing more to say.
	Touches []string
	// Paths lists the files under the rendered harness directory the edit
	// owns, relative to that directory's root and slash separated. A memory
	// or skill edit names exactly one file and its Body is that file's
	// content; a system-prompt edit names system-prompt.md and carries its
	// content as a sidecar diff instead.
	//
	// It is optional here and required by whatever renders the edit: an edit
	// document is a proposal, and DocDag has no path arithmetic to check one
	// against its component, so the obligation is carried by CheckPaths and
	// by the two commands that call it.
	Paths []string
	// DiffSHA256 is the SHA-256 of the sidecar unified diff, as 64 lowercase
	// hexadecimal digits. Only a system-prompt edit writes one.
	DiffSHA256 string
	// RootCause is prose: why the failure happened, not what to do about it.
	RootCause string
	// Approval says whether a person stood behind the acceptance.
	Approval vocab.Approval
	// ApprovedBy names that person.
	ApprovedBy string
	// InForceFrom and InForceUntil are the days the edit carries force between.
	// An edit naming neither is in force from the beginning with no end.
	InForceFrom  string
	InForceUntil string
	// About names the topics the edit speaks to. Every edit in force states at
	// least one.
	About []string
	// Premise names what the edit rests on being true.
	Premise []string
	// Counterexample names the post-mortems that motivated it.
	Counterexample []string
	// Predicts is the falsifiable half of the proposal.
	Predicts []Prediction
	// Supersedes names the edits this one replaces.
	Supersedes []Supersession
	// Body is the Markdown under the heading. For a memory or a skill edit it
	// is not commentary about the change: it *is* the content written to the
	// single path the edit names. For a system-prompt edit the content is the
	// sidecar diff and the body is whatever prose the proposer wrote.
	Body string
}

// EditFrontmatter is an edit's frontmatter in the order it is written.
type EditFrontmatter struct {
	Kind      string `yaml:"kind"`
	Title     string `yaml:"title"`
	Status    string `yaml:"status"`
	Date      string `yaml:"date"`
	Component string `yaml:"component"`
	// touches carries no omitempty: an edit with an empty list is a document
	// the vault reports, and a writer that dropped the key would turn a
	// validation failure here into a lint failure there.
	Touches []string `yaml:"touches"`
	// paths carries omitempty: an edit that names no file is a proposal
	// nothing has rendered yet, which is a legal state for a document even
	// though it is not a legal state for a run.
	Paths          []string          `yaml:"paths,omitempty"`
	DiffSHA256     string            `yaml:"diff_sha256,omitempty"`
	RootCause      string            `yaml:"root_cause,omitempty"`
	Approval       string            `yaml:"approval"`
	ApprovedBy     string            `yaml:"approved_by,omitempty"`
	InForceFrom    string            `yaml:"in_force_from,omitempty"`
	InForceUntil   string            `yaml:"in_force_until,omitempty"`
	About          []string          `yaml:"about,omitempty"`
	Premise        []string          `yaml:"premise,omitempty"`
	Counterexample []string          `yaml:"counterexample,omitempty"`
	Predicts       []predictsEntry   `yaml:"predicts,omitempty"`
	Supersedes     []supersedesEntry `yaml:"supersedes,omitempty"`
}

// ID returns the edit's identifier.
func (e Edit) ID() string { return e.EditID }

// Kind returns the kind an edit answers to.
func (e Edit) Kind() vocab.Kind { return vocab.KindEdit }

// Path returns where the edit is written, relative to the vault root.
func (e Edit) Path() (string, error) { return vocab.Path(vocab.KindEdit, e.EditID) }

// Touching returns the edit with its blast radius set to the surfaces named,
// or to the edit's own component where none are: an edit changes at least the
// surface it is about, and that is the smallest radius there is to state.
func (e Edit) Touching(names ...string) Edit {
	if len(names) == 0 {
		names = []string{e.Component}
	}
	e.Touches = names
	return e
}

// Validate reports the first thing about the edit DocDag would refuse: an
// identifier of the wrong shape, a word outside a closed vocabulary, a surface
// CMoA does not declare, a required key left empty, or a day that is not a day.
func (e Edit) Validate() error {
	if !vocab.ValidEditID(e.EditID) {
		return fmt.Errorf("%w: %q is not an edit identifier (want %s)", ErrDocument, e.EditID, vocab.EditIDPattern)
	}
	if err := requireText("edit "+e.EditID+" title", e.Title); err != nil {
		return err
	}
	if err := requireDay("edit "+e.EditID+" date", e.Date); err != nil {
		return err
	}
	if err := requireVocabulary("edit "+e.EditID+" status", e.Status, vocab.EditStatuses()); err != nil {
		return err
	}
	if err := requireSurface("edit "+e.EditID+" component", e.Component); err != nil {
		return err
	}
	all, err := surfaces.All()
	if err != nil {
		return err
	}
	// An edit that states no blast radius is one edit_touches_readonly reports
	// as an error, because subset_of answers false on an absent key. The
	// obligation is the vault's; refusing to write the document is how this
	// package keeps the harness from creating the finding in the first place.
	if len(e.Touches) == 0 {
		return fmt.Errorf(
			"%w: edit %s writes no touches:; every edit states the surfaces it changes, at least its own component",
			ErrDocument, e.EditID)
	}
	for _, surface := range e.Touches {
		if !slices.Contains(all, surface) {
			return fmt.Errorf("%w: edit %s touches %q, which is not one of the seven harness surfaces",
				ErrDocument, e.EditID, surface)
		}
	}
	if err := requireVocabulary("edit "+e.EditID+" approval", e.Approval, vocab.AllApprovals()); err != nil {
		return err
	}
	// The paths are checked for shape here and for meaning in CheckPaths: a
	// document that names a file outside the tree is malformed whatever it is
	// for, while a document that names no file at all is merely unrendered.
	for _, p := range e.Paths {
		if err := surfaces.CheckHarnessPath(p); err != nil {
			return fmt.Errorf("%w: edit %s: %w", ErrDocument, e.EditID, err)
		}
	}
	if e.DiffSHA256 != "" && !hexDigest.MatchString(e.DiffSHA256) {
		return fmt.Errorf("%w: edit %s diff_sha256 %q is not 64 lowercase hexadecimal digits",
			ErrDocument, e.EditID, e.DiffSHA256)
	}
	if err := optionalDay("edit "+e.EditID+" in_force_from", e.InForceFrom); err != nil {
		return err
	}
	if err := optionalDay("edit "+e.EditID+" in_force_until", e.InForceUntil); err != nil {
		return err
	}
	if err := validateIDs("edit "+e.EditID+" about", e.About, ValidTopicID, TopicIDPattern); err != nil {
		return err
	}
	if err := validateIDs("edit "+e.EditID+" premise", e.Premise, ValidPremiseID, PremiseIDPattern); err != nil {
		return err
	}
	if err := validateIDs("edit "+e.EditID+" counterexample", e.Counterexample, ValidPostMortemID, PostMortemIDPattern); err != nil {
		return err
	}
	for _, prediction := range e.Predicts {
		if err := prediction.validate(e.EditID); err != nil {
			return err
		}
	}
	for _, supersession := range e.Supersedes {
		if err := supersession.validate(e.EditID); err != nil {
			return err
		}
	}
	return nil
}

// Frontmatter returns the edit's frontmatter.
func (e Edit) Frontmatter() (EditFrontmatter, error) {
	if err := e.Validate(); err != nil {
		return EditFrontmatter{}, err
	}
	front := EditFrontmatter{
		Kind:           vocab.KindEdit.String(),
		Title:          e.Title,
		Status:         e.Status.String(),
		Date:           e.Date,
		Component:      e.Component,
		Touches:        e.Touches,
		Paths:          e.Paths,
		DiffSHA256:     e.DiffSHA256,
		RootCause:      e.RootCause,
		Approval:       e.Approval.String(),
		ApprovedBy:     e.ApprovedBy,
		InForceFrom:    e.InForceFrom,
		InForceUntil:   e.InForceUntil,
		About:          e.About,
		Premise:        e.Premise,
		Counterexample: e.Counterexample,
	}
	for _, prediction := range e.Predicts {
		front.Predicts = append(front.Predicts, predictsEntry{
			Ref:     prediction.Pattern,
			Expect:  prediction.Expect.String(),
			Outcome: prediction.Outcome.String(),
		})
	}
	for _, supersession := range e.Supersedes {
		front.Supersedes = append(front.Supersedes, supersedesEntry{
			Ref:    supersession.Edit,
			Reason: supersession.Reason,
		})
	}
	return front, nil
}

// Bytes returns the document as it is written to disk.
func (e Edit) Bytes() ([]byte, error) {
	front, err := e.Frontmatter()
	if err != nil {
		return nil, err
	}
	return render(front, e.Title, e.Body)
}

// validate holds a prediction to the pattern it names and the two words the
// predicts edge declares.
func (p Prediction) validate(edit string) error {
	if !vocab.ValidPatternID(p.Pattern) {
		return fmt.Errorf("%w: edit %s predicts %q, which is not a pattern identifier (want %s)",
			ErrDocument, edit, p.Pattern, vocab.PatternIDPattern)
	}
	if err := requireVocabulary("edit "+edit+" predicts expect", p.Expect, vocab.AllExpects()); err != nil {
		return err
	}
	// The outcome is written when the prediction is settled, which is later
	// than when it is made, so an unwritten one is the ordinary case.
	if p.Outcome == "" {
		return nil
	}
	return requireVocabulary("edit "+edit+" predicts outcome", p.Outcome, vocab.AllOutcomes())
}

// validate holds a supersession to the edit it names and to the reason
// vocabulary the spec preset declares on the supersedes edge.
func (s Supersession) validate(edit string) error {
	if !vocab.ValidEditID(s.Edit) {
		return fmt.Errorf("%w: edit %s supersedes %q, which is not an edit identifier (want %s)",
			ErrDocument, edit, s.Edit, vocab.EditIDPattern)
	}
	if !slices.Contains(config.SupersedesReasons, s.Reason) {
		return fmt.Errorf("%w: edit %s supersedes %s for %q, which is outside %v",
			ErrDocument, edit, s.Edit, s.Reason, config.SupersedesReasons)
	}
	return nil
}

// validateIDs holds every reference in a list to one shape, naming the shape it
// wanted: a list of identifiers is the one place a caller cannot tell from the
// value which vocabulary it was read against.
func validateIDs(what string, ids []string, valid func(string) bool, want string) error {
	for _, id := range ids {
		if !valid(id) {
			return fmt.Errorf("%w: %s names %q, which is not an identifier of the shape %s",
				ErrDocument, what, id, want)
		}
	}
	return nil
}

// hexDigest is a SHA-256 as the frontmatter writes one.
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CheckPaths reports whether the edit says enough about files for something to
// render it. It is deliberately not part of Validate: an edit that names no
// path is a document DocDag accepts and a run refuses, and collapsing the two
// would make a legal corpus unwritable.
//
// The rule it enforces is the one the vault cannot: every path maps to exactly
// one surface by its shape, every path maps to the edit's own component,
// touches says the same set of surfaces the paths do, a memory or skill edit
// owns exactly one file, and a system-prompt edit carries the digest of the
// sidecar diff that holds its content while the other two carry none.
func (e Edit) CheckPaths() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if !surfaces.HasInjectionPoint(e.Component) {
		return fmt.Errorf(
			"%w: edit %s is about %q, which the rendered harness has no injection point for (%v)",
			ErrDocument, e.EditID, e.Component, surfaces.Injectable())
	}
	if len(e.Paths) == 0 {
		return fmt.Errorf("%w: edit %s names no paths:; an edit states the files it owns before it can be rendered",
			ErrDocument, e.EditID)
	}
	seen := map[string]bool{}
	for i, p := range e.Paths {
		if slices.Contains(e.Paths[:i], p) {
			return fmt.Errorf("%w: edit %s names path %q twice", ErrDocument, e.EditID, p)
		}
		component, err := surfaces.ComponentForPath(p)
		if err != nil {
			return fmt.Errorf("%w: edit %s: %w", ErrDocument, e.EditID, err)
		}
		if component != e.Component {
			return fmt.Errorf("%w: edit %s is about %q but owns %q, which is %q",
				ErrDocument, e.EditID, e.Component, p, component)
		}
		seen[component] = true
	}
	derived := slices.Sorted(maps.Keys(seen))
	touches := slices.Clone(e.Touches)
	slices.Sort(touches)
	touches = slices.Compact(touches)
	if !slices.Equal(derived, touches) {
		return fmt.Errorf("%w: edit %s touches %v but its paths say %v; touches is derived from paths and must match",
			ErrDocument, e.EditID, touches, derived)
	}
	switch e.Component {
	case componentSystemPrompt:
		if e.DiffSHA256 == "" {
			return fmt.Errorf("%w: edit %s is a system-prompt edit and writes no diff_sha256:; its content is the sidecar diff",
				ErrDocument, e.EditID)
		}
	default:
		if len(e.Paths) != 1 {
			return fmt.Errorf("%w: edit %s owns %d files; a %s edit owns exactly one, whose content is the document body",
				ErrDocument, e.EditID, len(e.Paths), e.Component)
		}
		if e.DiffSHA256 != "" {
			return fmt.Errorf("%w: edit %s is a %s edit and writes diff_sha256:; only a system-prompt edit carries a sidecar diff",
				ErrDocument, e.EditID, e.Component)
		}
		if strings.TrimSpace(e.Body) == "" {
			return fmt.Errorf("%w: edit %s has an empty body; a %s edit's body is the content of %s",
				ErrDocument, e.EditID, e.Component, e.Paths[0])
		}
	}
	return nil
}

// componentSystemPrompt is the one surface whose content lives beside the
// document rather than inside it.
const componentSystemPrompt = "system-prompt"

// DiffPath returns where a system-prompt edit's sidecar unified diff is
// written, relative to the vault root. The sidecar is not a document: DocDag
// parses only .md, so the file is invisible to the graph and the frontmatter
// digest is what stands guard over its bytes.
func DiffPath(editID string) (string, error) {
	if !vocab.ValidEditID(editID) {
		return "", fmt.Errorf("%w: %q is not an edit identifier (want %s)", ErrDocument, editID, vocab.EditIDPattern)
	}
	return vocab.DirEdits + "/" + editID + ".diff", nil
}

// DiffSHA256Of returns the digest an edit's diff_sha256 has to carry for these
// bytes, so a writer and a checker compute it the same way.
func DiffSHA256Of(diff []byte) string {
	sum := sha256.Sum256(diff)
	return hex.EncodeToString(sum[:])
}
