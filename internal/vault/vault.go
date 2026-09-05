// Package vault assembles uzushio's DocDag configuration in Go and writes it
// out as docdag.yaml. The file on disk is generated: it is long, every one of
// its rules is an argument, and an argument belongs beside the code that can
// test it. What lives here is the spec preset plus the four kinds uzushio
// adds — the edit a proposer writes, the failure pattern it answers, the
// evaluation run that settles it, and the health check that says whether the
// verifier behind the run was worth believing — with the edges, projections and
// rules that make an accepted edit mean something.
package vault

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Kaikei-e/DocDag/config"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ErrConfig is the sentinel every assembly failure wraps.
var ErrConfig = errors.New("vault: invalid configuration")

// referencePattern is the wikilink shape the reference layer gates on. It is
// the shape the vault had before uzushio's kinds existed, with the four new
// identifiers added: a token the pattern rejects is dropped without a finding,
// so a kind missing from here is a kind whose wikilinks are never checked.
func referencePattern() string {
	alternatives := []string{
		`UZ-[A-Z]-\d{3}`,
		`conform/[a-z0-9-]+`,
		`dev-\d{4}`,
		`interp/.+`,
		`premise/.+`,
		`principle/.+`,
		`pm-\d{4}`,
		`topic/.+`,
		vocab.EditIDBody,
		vocab.PatternIDBody,
		vocab.RunIDBody,
		vocab.VerifierIDBody,
	}
	pattern := "^("
	for i, alternative := range alternatives {
		if i > 0 {
			pattern += "|"
		}
		pattern += alternative
	}
	return pattern + ")$"
}

// eq and not build the two scalar attribute operands. A projection reads as
// the strings "true" and "false" on every document, so negating one is
// eq("false") rather than a not: block — not: would also hold on documents of
// every other kind, where the projection is simply absent.
func eq(value string) config.AttrCondition {
	v := value
	return config.AttrCondition{Eq: &v}
}

// isTrue and isFalse read a projection.
func isTrue() config.AttrCondition  { return eq(config.ProjectionTrue) }
func isFalse() config.AttrCondition { return eq(config.ProjectionFalse) }

// ofKind opens every uzushio rule: a document's kind is the directory's
// answer, and every rule here is about one kind's documents.
func ofKind(k vocab.Kind) map[string]config.AttrCondition {
	return map[string]config.AttrCondition{config.KeyKind: eq(k.String())}
}

// componentAnyOf builds the disjunction "the document's component is one of
// these surfaces". There is no operator for membership on a scalar, so the
// vocabulary is enumerated; the list comes from CMoA rather than from a
// literal here, so a surface that changes autonomy changes the rule.
func componentAnyOf(names []string) []config.Condition {
	out := make([]config.Condition, 0, len(names))
	for _, name := range names {
		out = append(out, config.Condition{
			Attr: map[string]config.AttrCondition{vocab.FieldComponent.String(): eq(name)},
		})
	}
	return out
}

// Config returns the effective uzushio configuration: the spec preset with
// uzushio's kinds, edges, projections and rules on top. It validates what it
// built — DocDag's own Validate, and the two checks Validate does not make —
// so a caller that gets a Config back has one the engine will accept.
func Config() (config.Config, error) {
	all, err := surfaces.All()
	if err != nil {
		return config.Config{}, err
	}
	proposeOnly, err := surfaces.ProposeOnly()
	if err != nil {
		return config.Config{}, err
	}
	humanApproval, err := surfaces.HumanApproval()
	if err != nil {
		return config.Config{}, err
	}

	cfg := config.SpecPreset()
	addKinds(&cfg, all)
	if err := extendEdges(&cfg); err != nil {
		return config.Config{}, err
	}
	if err := extendProjections(&cfg); err != nil {
		return config.Config{}, err
	}
	cfg.Rules = append(cfg.Rules, uzushioRules(all, proposeOnly, humanApproval)...)

	// The reference layer and the one structural check the vault raises, kept
	// from the configuration this file replaces: a document with no
	// frontmatter is not a document, and a wikilink that names nothing is an
	// error rather than a note.
	cfg.References = config.ReferencesSpec{
		Dangling: string(model.SeverityError),
		Pattern:  referencePattern(),
		Scan:     []string{config.ScanBody, config.ScanFrontmatter},
	}
	cfg.Structural = map[string]model.Severity{
		model.RuleMissingFrontmatter: model.SeverityError,
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	if err := SelfCheck(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// addKinds declares the four uzushio kinds.
//
// All four are append_only. The history check reads only kinds that opt in,
// and it exempts the status field, so marking an edit append_only does not
// stand in the way of its status moving from proposed to accepted; what it
// forbids is rewriting a decision after the fact. It protects less than it
// sounds like — only documents whose committed status is accepted, superseded
// or withdrawn, so runs and rejected edits are outside it — which is why the
// workflow also refuses deletions outright.
func addKinds(cfg *config.Config, allSurfaces []string) {
	component := config.FieldSpec{OneOf: slices.Clone(allSurfaces), Required: true}

	cfg.Kinds[vocab.KindEdit.String()] = config.KindSpec{
		Dir:          vocab.DirEdits,
		ID:           vocab.EditIDPattern,
		StatusValues: vocab.Strings(vocab.EditStatuses()),
		Closed:       true,
		AppendOnly:   true,
		Fields: map[string]config.FieldSpec{
			vocab.FieldComponent.String(): component,
			// touches is a list, and a list value reads as absent to the
			// scalar check behind `required`, so declaring it required would
			// report every edit that writes one. The obligation is carried by
			// the edit_touches_readonly rule instead.
			vocab.FieldTouches.String(): {},
			// paths is a list too, and DocDag has no path arithmetic: it
			// cannot test that memory/note.md belongs to the memory surface.
			// The check is uzushio's, in `harness render` and `run`, which
			// refuse an edit with no paths and an edit whose paths and
			// touches disagree. Declaring the key keeps the kind closed.
			vocab.FieldPaths.String(): {},
			// diff_sha256 is written only by a system-prompt edit, whose
			// content is a sidecar .diff rather than the document body, so it
			// is optional here and required by the renderer.
			vocab.FieldDiffSHA256.String(): {},
			vocab.FieldRootCause.String():  {},
			// approved_by is the person's name, which is free text and so
			// cannot be tested for presence; approval is its closed companion,
			// and it is the one the rule reads.
			vocab.FieldApprovedBy.String(): {},
			vocab.FieldApproval.String(): {
				OneOf:    vocab.Strings(vocab.AllApprovals()),
				Required: true,
			},
			vocab.FieldInForceFrom.String():  {},
			vocab.FieldInForceUntil.String(): {},
		},
		// An edit carries force between the day it landed and the day it was
		// rolled back, spelled the way a clause spells it. An edit naming
		// neither day is in force from the beginning with no end in sight.
		Period: &config.PeriodSpec{
			From:  vocab.FieldInForceFrom.String(),
			Until: vocab.FieldInForceUntil.String(),
		},
	}

	cfg.Kinds[vocab.KindPattern.String()] = config.KindSpec{
		Dir:          vocab.DirPatterns,
		ID:           vocab.PatternIDPattern,
		StatusValues: vocab.Strings(vocab.PatternStatuses()),
		Closed:       true,
		AppendOnly:   true,
		Fields: map[string]config.FieldSpec{
			vocab.FieldCategory.String(): {
				OneOf:    vocab.Strings(vocab.AllCategories()),
				Required: true,
			},
			// STPA asks every unsafe control action to state the context it is
			// unsafe in; a pattern without one names a component and a verb
			// and says nothing about when it goes wrong.
			vocab.FieldContext.String():   {Required: true},
			vocab.FieldComponent.String(): component,
			vocab.FieldEvidence.String():  {},
		},
	}

	cfg.Kinds[vocab.KindRun.String()] = config.KindSpec{
		Dir: vocab.DirRuns,
		ID:  vocab.RunIDPattern,
		// A run answers to no status vocabulary: it is a measurement, and
		// there is nothing about a measurement to accept or withdraw.
		Closed:     true,
		AppendOnly: true,
		Fields: map[string]config.FieldSpec{
			vocab.FieldSplit.String(): {
				OneOf:    vocab.Strings(vocab.AllSplits()),
				Required: true,
			},
			vocab.FieldVerdict.String(): {
				OneOf:    vocab.Strings(vocab.AllVerdicts()),
				Required: true,
			},
			// A pass rate is a number about nothing until the suite it was
			// measured over is named.
			vocab.FieldSuite.String():  {Required: true},
			vocab.FieldTrials.String(): {},
			vocab.FieldTrace.String():  {},
		},
	}

	cfg.Kinds[vocab.KindVerifier.String()] = config.KindSpec{
		Dir: vocab.DirVerifiers,
		ID:  vocab.VerifierIDPattern,
		// Like a run, a verifier health check answers to no status
		// vocabulary: what it says is a measurement of the verifier, and a
		// measurement is not accepted or withdrawn. What it concluded is the
		// verdict, and the verdict is a reading rather than a decision.
		Closed:     true,
		AppendOnly: true,
		Fields: map[string]config.FieldSpec{
			vocab.FieldVerdict.String(): {
				OneOf:    vocab.Strings(vocab.AllHealths()),
				Required: true,
			},
			// The rate and the two counts are strings: a scalar field is
			// compared as text, and the numbers a machine reads are in the
			// report the `report` key names.
			vocab.FieldKillRate.String():      {},
			vocab.FieldMutants.String():       {},
			vocab.FieldReferenceRuns.String(): {},
			vocab.FieldReport.String():        {},
		},
		// No edges and no rules this step. A verifier check has nothing to
		// point at yet: the task it is about is CMoA's, not a document in this
		// vault, and the clause that will require one is Step 4's work.
	}
}

// extendEdges adds uzushio's two edges and widens the four preset edges an
// edit takes part in. The preset declares each edge on the side that generates
// it, so widening `from` is how a new kind joins an existing relation.
func extendEdges(cfg *config.Config) error {
	widen := []struct {
		edge model.EdgeType
		from []string
		to   []string
	}{
		// An edit replaces an edit, the way a clause replaces a clause. The
		// reason attribute the preset already requires applies unchanged.
		{config.EdgeSupersedes, []string{vocab.KindEdit.String()}, []string{vocab.KindEdit.String()}},
		// An edit rests on premises, names the post-mortem that motivated it,
		// and states its subject — the last of which the preset already
		// requires of every document the about edge starts at.
		{config.EdgePremise, []string{vocab.KindEdit.String()}, nil},
		{config.EdgeCounterexample, []string{vocab.KindEdit.String()}, nil},
		{config.EdgeAbout, []string{vocab.KindEdit.String()}, nil},
	}
	for _, w := range widen {
		index := slices.IndexFunc(cfg.Edges, func(spec config.EdgeSpec) bool {
			return spec.Name == w.edge.String()
		})
		if index < 0 {
			return fmt.Errorf("%w: the spec preset declares no %q edge to widen", ErrConfig, w.edge)
		}
		spec := cfg.Edges[index]
		spec.From = appendKinds(spec.From, w.from)
		spec.To = appendKinds(spec.To, w.to)
		cfg.Edges[index] = spec
	}

	cfg.Edges = append(cfg.Edges,
		config.EdgeSpec{
			// The falsifiable half of a proposal. An edit that predicts
			// nothing cannot be wrong about anything, which is why
			// edit_without_prediction is an error rather than a note.
			Name:      vocab.EdgePredicts.String(),
			Key:       vocab.EdgePredicts.String(),
			Direction: config.DirectionForward,
			From:      []string{vocab.KindEdit.String()},
			To:        []string{vocab.KindPattern.String()},
			Attrs: map[string]config.EdgeAttrSpec{
				vocab.AttrExpect.String(): {
					Required: true,
					OneOf:    vocab.Strings(vocab.AllExpects()),
				},
				// The outcome is written when the prediction is settled,
				// which is later than when it is made, so it is optional.
				vocab.AttrOutcome.String(): {
					OneOf: vocab.Strings(vocab.AllOutcomes()),
				},
			},
		},
		config.EdgeSpec{
			Name:      vocab.EdgeValidates.String(),
			Key:       vocab.EdgeValidates.String(),
			Direction: config.DirectionForward,
			From:      []string{vocab.KindRun.String()},
			To:        []string{vocab.KindEdit.String()},
			Attrs: map[string]config.EdgeAttrSpec{
				vocab.AttrModel.String(): {Required: true, Type: config.AttrTypeString},
				// A rate without its baseline says nothing about a change, so
				// both are required and both are numbers.
				vocab.AttrPassRate.String():         {Required: true, Type: config.AttrTypeNumber},
				vocab.AttrBaselinePassRate.String(): {Required: true, Type: config.AttrTypeNumber},
			},
			// No target condition, deliberately. `leaf_of` is a liveness
			// constraint — "the document this points at must still be the
			// current one" — and a run is not a live pointer: it is
			// append-only history of what was measured, on the day it was
			// measured. The moment the edit it measured is superseded, which
			// is the ordinary lifecycle, a `leaf_of` target would make every
			// historical run a stale_target error for the rest of the
			// repository's life. There would be no legal move out of it: the
			// run may not be retargeted or deleted (append_only, and the
			// workflow refuses the diff), and stale_target is a structural
			// check, which a configuration may raise but never lower.
			//
			// DocDag names this failure mode and offers exactly one escape —
			// internal/graph/target.go's declaredInForce skips the check for
			// an out-of-force declaring document, "because append-first
			// history keeps the record, and a history that cannot be added to
			// without breaking the build is a ratchet rather than an archive".
			// The escape needs a period on the declaring kind, and a
			// measurement must not have one: a run does not expire.
			//
			// The same reasoning keeps `predicts` targetless.
		},
	)
	return nil
}

// appendKinds adds kinds to an endpoint list, skipping the ones already there
// so widening an edge twice is the same as widening it once.
func appendKinds(have, add []string) []string {
	for _, kind := range add {
		if !slices.Contains(have, kind) {
			have = append(have, kind)
		}
	}
	return have
}

// extendProjections adds the three projections that say what a run has shown
// about an edit, and teaches the binding projection what an effective edit is.
func extendProjections(cfg *config.Config) error {
	validated := func(name vocab.Projection, split vocab.Split) config.ProjectionSpec {
		alternative := func(verdict vocab.Verdict) config.ProjectionAlt {
			return config.ProjectionAlt{When: config.Condition{
				ViaInbound: &config.ViaCondition{
					Edge: vocab.EdgeValidates.String(),
					Attr: map[string]config.AttrCondition{
						vocab.FieldSplit.String():   eq(split.String()),
						vocab.FieldVerdict.String(): eq(verdict.String()),
					},
				},
			}}
		}
		// "Did not regress on this split" is two verdicts rather than one, and
		// a condition holds one not: block, so the pair is written as
		// alternatives instead of as the negation of regress — which would
		// also hold where no run exists at all.
		return config.ProjectionSpec{
			Name: name.String(),
			AnyOf: []config.ProjectionAlt{
				alternative(vocab.VerdictImprove),
				alternative(vocab.VerdictHold),
			},
		}
	}

	cfg.Projections = append(cfg.Projections,
		validated(vocab.ProjectionValidatedIn, vocab.SplitHeldIn),
		validated(vocab.ProjectionValidatedOut, vocab.SplitHeldOut),
		config.ProjectionSpec{
			// A strict gain on either split. Held-out is the one that matters
			// for generalisation, but an edit that improves the held-in split
			// and holds the held-out one has still shown a gain, and the
			// acceptance rule reads the pair together.
			Name: vocab.ProjectionImproved.String(),
			When: config.Condition{
				ViaInbound: &config.ViaCondition{
					Edge: vocab.EdgeValidates.String(),
					Attr: map[string]config.AttrCondition{
						vocab.FieldVerdict.String(): eq(vocab.VerdictImprove.String()),
					},
				},
			},
		},
	)

	// What binding means for an edit: accepted, in force today, not already
	// replaced, and carrying the three things a run can show. Alternatives are
	// OR-ed and this one is scoped to a kind the preset's alternatives never
	// reach, so adding it cannot change what `effective` says about a clause.
	index := slices.IndexFunc(cfg.Projections, func(spec config.ProjectionSpec) bool {
		return spec.Name == config.ProjectionEffective
	})
	if index < 0 {
		return fmt.Errorf("%w: the spec preset declares no %q projection", ErrConfig, config.ProjectionEffective)
	}
	effective := cfg.Projections[index]
	effective.AnyOf = append(effective.AnyOf, config.ProjectionAlt{When: config.Condition{
		Attr: map[string]config.AttrCondition{
			config.KeyKind:                        eq(vocab.KindEdit.String()),
			config.DefaultStatusField:             eq(vocab.StatusAccepted.String()),
			vocab.ProjectionValidatedIn.String():  isTrue(),
			vocab.ProjectionValidatedOut.String(): isTrue(),
			vocab.ProjectionImproved.String():     isTrue(),
			config.AttrInForce:                    isTrue(),
		},
		Not: &config.Condition{Attr: map[string]config.AttrCondition{
			config.ProjectionInForceSuccessor: isTrue(),
		}},
	}})
	cfg.Projections[index] = effective
	return nil
}

// uzushioRules returns the rules uzushio adds, in the order the generated file
// declares them. Every one of them opens with the kind it is about: a rule
// without that clause is a rule about every document in the vault.
func uzushioRules(allSurfaces, proposeOnly, humanApproval []string) []config.Rule {
	editAttrs := func(extra map[string]config.AttrCondition) map[string]config.AttrCondition {
		attrs := ofKind(vocab.KindEdit)
		for key, cond := range extra {
			attrs[key] = cond
		}
		return attrs
	}
	status := func(s vocab.Status) map[string]config.AttrCondition {
		return map[string]config.AttrCondition{config.DefaultStatusField: eq(s.String())}
	}

	return []config.Rule{
		{
			// Self-Harness's acceptance test, written out: no regression on
			// the held-in split, no regression on the held-out split, and a
			// strict gain somewhere. A condition holds one not: block, so the
			// three are written as alternatives of the projections reading
			// "false" — which is what a projection says on a document it does
			// not hold for, including one no run has ever touched.
			Name:     vocab.RuleAcceptedUnvalidated.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr: editAttrs(status(vocab.StatusAccepted)),
				AnyOf: []config.Condition{
					{Attr: map[string]config.AttrCondition{vocab.ProjectionValidatedIn.String(): isFalse()}},
					{Attr: map[string]config.AttrCondition{vocab.ProjectionValidatedOut.String(): isFalse()}},
					{Attr: map[string]config.AttrCondition{vocab.ProjectionImproved.String(): isFalse()}},
				},
			},
			Message: "is accepted without a non-regressing held-in run, a non-regressing held-out run and a strict improvement",
		},
		{
			// subset_of is the one operand that reads a list, and it answers
			// false where the key is absent — so this fires both on an edit
			// that touches something outside the seven surfaces and on an edit
			// that lists nothing at all. Both are the same mistake: an edit
			// whose blast radius is not written down.
			Name:     vocab.RuleEditTouchesReadonly.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr: ofKind(vocab.KindEdit),
				Not: &config.Condition{Attr: map[string]config.AttrCondition{
					vocab.FieldTouches.String(): {SubsetOf: slices.Clone(allSurfaces)},
				}},
			},
			Message: "must list touches: within the seven harness surfaces; verifier, tracer and model-config are read-only",
		},
		{
			// An edge declared by an out-of-force document is dropped from the
			// index, so a scheduled edit would fire this on the strength of
			// its start day alone. The in_force clause is what keeps the rule
			// about edits that are actually running.
			Name:     vocab.RuleEditWithoutPrediction.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr:        editAttrs(map[string]config.AttrCondition{config.AttrInForce: isTrue()}),
				NotOutbound: vocab.EdgePredicts.String(),
			},
			Message: "predicts nothing; declare predicts: with expect: fix or at-risk",
		},
		{
			// A rejection is kept with its evidence. Self-Harness logs the
			// candidates it turned down, because the reason a change did not
			// work is the part a later proposer needs.
			Name:     vocab.RuleRejectedWithoutRun.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr:       editAttrs(status(vocab.StatusRejected)),
				NotInbound: vocab.EdgeValidates.String(),
			},
			Message: "was rejected with no run recorded; a rejection is kept with its evidence",
		},
		{
			// The surfaces CMoA marks propose-only are the ones an agent may
			// write a patch for and never merge. The list is enumerated
			// because there is no membership operand for a scalar.
			Name:     vocab.RuleProposeOnlyAccepted.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr:  editAttrs(status(vocab.StatusAccepted)),
				AnyOf: componentAnyOf(proposeOnly),
			},
			Message: "edits a propose-only surface and cannot be accepted",
		},
		{
			// The closest sound form of "accepted without a person": DocDag
			// has no presence test for a free-text field, so the rule reads
			// the closed approval word rather than the approver's name.
			// approval is required on every edit, so an edit that writes
			// neither key is reported by missing_field instead.
			Name:     vocab.RuleAcceptedWithoutApprover.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr: editAttrs(map[string]config.AttrCondition{
					config.DefaultStatusField:    eq(vocab.StatusAccepted.String()),
					vocab.FieldApproval.String(): eq(vocab.ApprovalAuto.String()),
				}),
				AnyOf: componentAnyOf(humanApproval),
			},
			Message: "edits a human-approval surface but was accepted without a person; set approval: human and name the approver in approved_by",
		},
		{
			// A pattern is resolved when something fixed it. The via clause
			// reads the neighbour's status rather than the edge's expect
			// attribute: a one-hop clause carries attributes of the document
			// at the other end, and an edge attribute is not among them.
			Name:     vocab.RulePatternResolvedUnfixed.String(),
			Severity: model.SeverityWarn,
			When: config.Condition{
				Attr: map[string]config.AttrCondition{
					config.KeyKind:            eq(vocab.KindPattern.String()),
					config.DefaultStatusField: eq(vocab.StatusResolved.String()),
				},
				Not: &config.Condition{ViaInbound: &config.ViaCondition{
					Edge: vocab.EdgePredicts.String(),
					Attr: status(vocab.StatusAccepted),
				}},
			},
			Message: "is resolved but no accepted edit predicted it",
		},
		{
			// A live edit aimed at a pattern nobody believes in any more. A
			// warning: the edit may still be worth keeping, but what it claims
			// no longer describes anything.
			Name:     vocab.RulePredictsWithdrawn.String(),
			Severity: model.SeverityWarn,
			When: config.Condition{
				Attr: ofKind(vocab.KindEdit),
				AnyOf: []config.Condition{
					{Attr: status(vocab.StatusProposed)},
					{Attr: status(vocab.StatusAccepted)},
				},
				Via: &config.ViaCondition{
					Edge: vocab.EdgePredicts.String(),
					Attr: status(vocab.StatusWithdrawn),
				},
			},
			Message: "predicts a withdrawn pattern",
		},
		{
			// The about edge already carries min_outbound: 1, so the engine's
			// cardinality check reports an edit with no topic on its own. The
			// named rule is here anyway, because a finding named
			// edit_without_topic is the one a reader can act on, and because
			// the cardinality finding is the edge's rather than the kind's.
			// The two overlap by design; the in_force clause keeps this one
			// off scheduled edits, whose edges are not in the index yet.
			Name:     vocab.RuleEditWithoutTopic.String(),
			Severity: model.SeverityError,
			When: config.Condition{
				Attr:        editAttrs(map[string]config.AttrCondition{config.AttrInForce: isTrue()}),
				NotOutbound: config.EdgeAbout.String(),
			},
			Message: "names no topic; every edit states the subject it is about",
		},
	}
}
