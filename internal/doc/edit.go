package doc

import (
	"fmt"
	"slices"

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
	// Body is the Markdown under the heading.
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
	Touches        []string          `yaml:"touches"`
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
