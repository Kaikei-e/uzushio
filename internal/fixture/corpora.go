package fixture

import (
	"fmt"
	"strings"

	"github.com/Kaikei-e/DocDag/config"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// The values every fixture document shares. A fixture is read as an argument
// about one rule, so everything that is not that argument is held constant.
const (
	// fixtureDay is the one day the corpora are written on. No fixture depends
	// on the day it is read, so a fixed day keeps the bytes stable.
	fixtureDay = "2026-01-01"
	// fixtureSuite is the task set a fixture's runs are measured over.
	fixtureSuite = "harness-tasks"
	// fixtureModel is the model slug a fixture's runs name, in the identifier
	// and in the validates entry both.
	fixtureModel = "sonnet"
	// fixtureApprover is the person an accepted fixture edit names.
	fixtureApprover = "the maintainer"
	// fixtureTrials is how many attempts a fixture's rates stand for.
	fixtureTrials = 40
	// fixtureTrace is the CMoA trace run a fixture pattern cites as evidence.
	// It is CMoA's identifier shape, not uzushio's: a pattern is a reading of
	// what the harness did, and what it did is in CMoA's traces.
	fixtureTrace = "20260101T000000Z-0f1e2d3c"
)

// exemplars are the surfaces the corpora argue with. They are read out of
// CMoA's vocabulary rather than written down here, because what each fixture
// needs is a surface with a given autonomy rather than a particular name: an
// edit to an auto-accept surface carries no approval obligation, one to a
// human-approval surface names a person, and one to a propose-only surface may
// never be accepted at all. A surface that changes autonomy in CMoA therefore
// changes these fixtures, which is what this package's claim to spell a word
// once is worth.
type exemplars struct {
	// auto is a surface whose edits the harness accepts by itself.
	auto string
	// alsoAuto is a second surface, for a blast radius wider than one. Any of
	// the seven will do: what the fixture shows is a subset, not an autonomy.
	alsoAuto string
	// human is a surface whose edits a person accepts.
	human string
	// proposeOnly is a surface whose edits are proposed and never accepted.
	proposeOnly string
	// readOnly is a component no edit may touch. It is not a surface at all,
	// which is exactly why edit_touches_readonly reports an edit naming it.
	readOnly string
}

func surfaceExemplars() (exemplars, error) {
	first := func(names []string, err error) (string, error) {
		if err != nil {
			return "", err
		}
		if len(names) == 0 {
			return "", fmt.Errorf("%w: CMoA declares no component of that kind", ErrFixture)
		}
		return names[0], nil
	}
	auto, err := first(surfaces.ByAutonomy(surfaces.AutonomyAutoAccept))
	if err != nil {
		return exemplars{}, err
	}
	human, err := first(surfaces.ByAutonomy(surfaces.AutonomyHumanApproval))
	if err != nil {
		return exemplars{}, err
	}
	proposeOnly, err := first(surfaces.ByAutonomy(surfaces.AutonomyProposeOnly))
	if err != nil {
		return exemplars{}, err
	}
	readOnly, err := first(surfaces.ReadOnly())
	if err != nil {
		return exemplars{}, err
	}
	all, err := surfaces.All()
	if err != nil {
		return exemplars{}, err
	}
	alsoAuto := ""
	for _, name := range all {
		if name != auto {
			alsoAuto = name
			break
		}
	}
	if alsoAuto == "" {
		return exemplars{}, fmt.Errorf("%w: CMoA declares only one surface", ErrFixture)
	}
	return exemplars{
		auto: auto, alsoAuto: alsoAuto, human: human, proposeOnly: proposeOnly, readOnly: readOnly,
	}, nil
}

// fixtureTopic is the subject every fixture edit that needs one is about.
var fixtureTopic = topic{
	slug:  "fixture-subject",
	title: "The subject a fixture edit is about",
	body: "A topic exists here only so an edit's about: edge reaches a document.\n" +
		"An edge whose target is not in the corpus is not an edge at all, so a\n" +
		"fixture that tests for a missing topic has to bring the topic with it.",
}

// anEdit is the edit every fixture starts from: in force from the beginning,
// touching only the surface it names, and approved by a person where it is
// accepted at all.
func anEdit(id string, status vocab.Status, component, title, body string) doc.Edit {
	edit := doc.Edit{
		EditID:    id,
		Title:     title,
		Date:      fixtureDay,
		Status:    status,
		Component: component,
		Approval:  vocab.ApprovalHuman,
		Body:      body,
	}.Touching()
	if status == vocab.StatusAccepted {
		edit.ApprovedBy = fixtureApprover
	}
	return edit
}

// aPattern is the failure pattern a fixture's edits point at.
func aPattern(id string, status vocab.Status, component, title, body string) doc.Pattern {
	return doc.Pattern{
		PatternID: id,
		Title:     title,
		Date:      fixtureDay,
		Status:    status,
		Category:  vocab.CategoryNotProvided,
		Context:   "the harness is asked for the same tool call twice in one task",
		Component: component,
		Evidence:  []string{fixtureTrace},
		Body:      body,
	}
}

// aRun is one evaluation of one edit. The rates follow the verdict rather than
// being written beside it, so a fixture cannot claim an improvement with a pass
// rate below its baseline.
func aRun(edit string, split vocab.Split, verdict vocab.Verdict, body string) doc.Run {
	const baseline = 0.60
	rate := baseline
	switch verdict {
	case vocab.VerdictImprove:
		rate = 0.72
	case vocab.VerdictHold:
		rate = baseline
	case vocab.VerdictRegress:
		rate = 0.48
	case vocab.VerdictInconclusive:
		rate = 0.62
	}
	return doc.Run{
		Edit:      edit,
		Day:       fixtureDay,
		ModelSlug: fixtureModel,
		Split:     split,
		Title:     fmt.Sprintf("%s on the %s split", edit, split),
		Date:      fixtureDay,
		Verdict:   verdict,
		Suite:     fixtureSuite,
		Trials:    fixtureTrials,
		Body:      body,
	}.Measuring(fixtureModel, rate, baseline)
}

// validating returns the two runs an edit needs to be accepted: a held-in run
// that gains and a held-out run that does not regress. Together they make
// validated_in, validated_out and improved all hold.
func validating(edit string) []written {
	return []written{
		aRun(edit, vocab.SplitHeldIn, vocab.VerdictImprove,
			"The split the edit was developed against gained over the baseline."),
		aRun(edit, vocab.SplitHeldOut, vocab.VerdictHold,
			"The split held back from development did not regress, which is what\n"+
				"acceptance asks of it."),
	}
}

// rawEdit writes an edit as text rather than through internal/doc, for the two
// documents internal/doc refuses to write: one that states no blast radius, and
// one that states a read-only component. Both are documents the vault reports,
// which is why they exist and why no writer may produce them. The key order is
// the one doc.EditFrontmatter declares, so the two kinds of document read the
// same way in a diff.
func rawEdit(id, title, component string, touches []string, body string) (raw, error) {
	relative, err := vocab.Path(vocab.KindEdit, id)
	if err != nil {
		return raw{}, err
	}
	block := ""
	if touches != nil {
		block = "touches:\n"
		for _, surface := range touches {
			block += "- " + surface + "\n"
		}
	}
	text := fmt.Sprintf(
		"---\nkind: %s\ntitle: %s\nstatus: %s\ndate: %q\ncomponent: %s\n%sapproval: %s\n---\n\n# %s\n\n%s\n",
		vocab.KindEdit, title, vocab.StatusProposed, fixtureDay, component, block,
		vocab.ApprovalHuman, title, strings.TrimRight(body, "\n"))
	return raw{relative: relative, text: text}, nil
}

// corpora is every fixture this package writes: the nine rules uzushio adds, in
// the order the configuration declares them, then the projections whose truth a
// rule reads, then the two preset projections uzushio's own edits can satisfy.
func corpora() ([]corpus, error) {
	s, err := surfaceExemplars()
	if err != nil {
		return nil, err
	}
	touchesReadonly, err := editTouchesReadonly(s)
	if err != nil {
		return nil, err
	}
	return []corpus{
		acceptedUnvalidated(s),
		touchesReadonly,
		editWithoutPrediction(s),
		rejectedWithoutRun(s),
		proposeOnlyAccepted(s),
		acceptedWithoutApprover(s),
		patternResolvedUnfixed(s),
		predictsWithdrawn(s),
		editWithoutTopic(s),
		validatedIn(s),
		validatedOut(s),
		improved(s),
		effective(s),
		hasInforceSuccessor(s),
	}, nil
}

// accepted_unvalidated: an accepted edit is missing one of the three things
// acceptance asks a run for.
func acceptedUnvalidated(s exemplars) corpus {
	return corpus{
		name: vocab.RuleAcceptedUnvalidated.String(),
		fires: []written{
			anEdit("he-9001", vocab.StatusAccepted, s.auto,
				"Accepted with nothing measured",
				"No run points at this edit, so validated_in, validated_out and\n"+
					"improved all read false and the rule reports all three at once."),
		},
		silent: append([]written{
			anEdit("he-9002", vocab.StatusAccepted, s.auto,
				"Accepted on a held-in gain and a held-out hold",
				"The three projections hold, which is the whole of what the rule asks."),
		}, validating("he-9002")...),
	}
}

// edit_touches_readonly: an edit whose blast radius is outside the seven
// surfaces, or is not written down at all. Both halves are on the fires side,
// because the rule's name says the first and its behaviour also covers the
// second — subset_of answers false on an absent key.
func editTouchesReadonly(s exemplars) (corpus, error) {
	unwritten, err := rawEdit("he-9003",
		"Proposed without saying what it touches", s.auto, nil,
		"No touches: key at all. subset_of answers false where the key is\n"+
			"absent, so an edit that states no blast radius is reported exactly as\n"+
			"one that states a radius outside the seven surfaces.")
	if err != nil {
		return corpus{}, err
	}
	readonly, err := rawEdit("he-9029",
		"Proposed against a read-only component", s.auto,
		[]string{s.auto, s.readOnly},
		"The case the rule is named after: the edit reaches past the seven\n"+
			"surfaces into a component the harness may read and never rewrite. It\n"+
			"is written here as text because internal/doc will not write it.")
	if err != nil {
		return corpus{}, err
	}
	spread := anEdit("he-9004", vocab.StatusProposed, s.auto,
		"Proposed across two surfaces it names",
		"Both surfaces are among the seven, so the blast radius is stated and\n"+
			"the rule has nothing to say.").Touching(s.auto, s.alsoAuto)

	return corpus{
		name:   vocab.RuleEditTouchesReadonly.String(),
		fires:  []written{unwritten, readonly},
		silent: []written{spread},
	}, nil
}

// edit_without_prediction: an edit in force that claims nothing.
func editWithoutPrediction(s exemplars) corpus {
	predicting := anEdit("he-9006", vocab.StatusProposed, s.auto,
		"Proposed against a pattern it names",
		"The edit says which failure it expects to remove, so it can be wrong,\n"+
			"which is the only way it can later be shown right.")
	predicting.Predicts = []doc.Prediction{{Pattern: "fp/fixture-pattern", Expect: vocab.ExpectFix}}

	return corpus{
		name: vocab.RuleEditWithoutPrediction.String(),
		fires: []written{
			anEdit("he-9005", vocab.StatusProposed, s.auto,
				"Proposed without a prediction",
				"An edit that predicts nothing cannot be wrong about anything."),
		},
		silent: []written{
			predicting,
			aPattern("fp/fixture-pattern", vocab.StatusOpen, s.auto,
				"The failure the fixture edit predicts",
				"The pattern is here because an edge whose target is missing is not\n"+
					"recorded, and an unrecorded edge would fire the rule."),
		},
	}
}

// rejected_without_run: a rejection kept without the evidence behind it.
func rejectedWithoutRun(s exemplars) corpus {
	return corpus{
		name: vocab.RuleRejectedWithoutRun.String(),
		fires: []written{
			anEdit("he-9007", vocab.StatusRejected, s.auto,
				"Rejected with nothing recorded",
				"The reason a change did not work is the part a later proposer needs,\n"+
					"and here it is nowhere."),
		},
		silent: []written{
			anEdit("he-9008", vocab.StatusRejected, s.auto,
				"Rejected on a measured regression",
				"The run that settled it stays in the corpus beside the rejection."),
			aRun("he-9008", vocab.SplitHeldIn, vocab.VerdictRegress,
				"The measurement the rejection rests on."),
		},
	}
}

// propose_only_accepted: an edit to a surface an agent may write a patch for
// and never merge.
func proposeOnlyAccepted(s exemplars) corpus {
	return corpus{
		name: vocab.RuleProposeOnlyAccepted.String(),
		fires: []written{
			anEdit("he-9009", vocab.StatusAccepted, s.proposeOnly,
				"Accepted on a propose-only surface",
				"CMoA marks this surface propose-only, so acceptance is not uzushio's\n"+
					"to record whatever the runs say."),
		},
		silent: []written{
			anEdit("he-9010", vocab.StatusProposed, s.proposeOnly,
				"Proposed on a propose-only surface",
				"The same surface in the state it is allowed to be in."),
		},
	}
}

// accepted_without_approver: an edit to a human-approval surface accepted with
// nobody behind it.
func acceptedWithoutApprover(s exemplars) corpus {
	unattended := anEdit("he-9011", vocab.StatusAccepted, s.human,
		"Accepted on a human-approval surface by the harness",
		"approval: auto on a surface whose edits a person accepts. The rule reads\n"+
			"the closed approval word rather than the approver's name, because there\n"+
			"is no way to ask whether free text was written at all.")
	unattended.Approval = vocab.ApprovalAuto
	unattended.ApprovedBy = ""

	return corpus{
		name:  vocab.RuleAcceptedWithoutApprover.String(),
		fires: []written{unattended},
		silent: []written{
			anEdit("he-9012", vocab.StatusAccepted, s.human,
				"Accepted on a human-approval surface by a person",
				"approval: human, and approved_by names who."),
		},
	}
}

// pattern_resolved_unfixed: a pattern called resolved that no accepted edit
// predicted.
func patternResolvedUnfixed(s exemplars) corpus {
	fixing := anEdit("he-9013", vocab.StatusAccepted, s.auto,
		"Accepted against the pattern it resolved",
		"The edit predicted the pattern and was accepted, which is what makes the\n"+
			"pattern's resolution something the corpus can point at.")
	fixing.Predicts = []doc.Prediction{{
		Pattern: "fp/resolved-fixed",
		Expect:  vocab.ExpectFix,
		Outcome: vocab.OutcomeConfirmed,
	}}

	return corpus{
		name: vocab.RulePatternResolvedUnfixed.String(),
		fires: []written{
			aPattern("fp/resolved-unfixed", vocab.StatusResolved, s.auto,
				"Resolved with nothing pointing at it",
				"Somebody closed the pattern by hand. Nothing in the corpus says what\n"+
					"fixed it, so the resolution is a claim rather than a record."),
		},
		silent: []written{
			fixing,
			aPattern("fp/resolved-fixed", vocab.StatusResolved, s.auto,
				"Resolved by an accepted edit",
				"The accepted edit that predicted this pattern is what closed it."),
		},
	}
}

// predicts_withdrawn: a live edit aimed at a pattern nobody believes in.
func predictsWithdrawn(s exemplars) corpus {
	aiming := anEdit("he-9014", vocab.StatusProposed, s.auto,
		"Proposed against a withdrawn pattern",
		"The edit may still be worth keeping, but what it claims no longer\n"+
			"describes anything.")
	aiming.Predicts = []doc.Prediction{{Pattern: "fp/withdrawn-pattern", Expect: vocab.ExpectFix}}

	live := anEdit("he-9015", vocab.StatusProposed, s.auto,
		"Proposed against an open pattern",
		"The pattern is still open, so the prediction is still about something.")
	live.Predicts = []doc.Prediction{{Pattern: "fp/open-pattern", Expect: vocab.ExpectFix}}

	return corpus{
		name: vocab.RulePredictsWithdrawn.String(),
		fires: []written{
			aiming,
			aPattern("fp/withdrawn-pattern", vocab.StatusWithdrawn, s.auto,
				"The pattern that turned out not to be one",
				"Withdrawn: on a closer look the failure was a property of one task\n"+
					"rather than of the harness."),
		},
		silent: []written{
			live,
			aPattern("fp/open-pattern", vocab.StatusOpen, s.auto,
				"The pattern still waiting for a fix",
				"Open, and seen often enough to be worth an edit."),
		},
	}
}

// edit_without_topic: an edit in force that names no subject.
func editWithoutTopic(s exemplars) corpus {
	subjected := anEdit("he-9017", vocab.StatusProposed, s.auto,
		"Proposed about a subject it names",
		"Two edits can only be seen to disagree where they are known to speak to\n"+
			"the same subject, so the subject is a document.")
	subjected.About = []string{"topic/" + fixtureTopic.slug}

	return corpus{
		name: vocab.RuleEditWithoutTopic.String(),
		fires: []written{
			anEdit("he-9016", vocab.StatusProposed, s.auto,
				"Proposed about nothing in particular",
				"No about: edge, so nothing in the corpus says what this edit speaks to."),
		},
		silent: []written{subjected, fixtureTopic},
	}
}

// validated_in: a held-in run that did not regress.
func validatedIn(s exemplars) corpus {
	return corpus{
		name: vocab.ProjectionValidatedIn.String(),
		fires: []written{
			anEdit("he-9018", vocab.StatusProposed, s.auto,
				"Measured on the held-in split",
				"A held-in run that held is enough for the projection: what it says is\n"+
					"that the split did not get worse, not that it got better."),
			aRun("he-9018", vocab.SplitHeldIn, vocab.VerdictHold,
				"No regression and no gain on the split the edit was developed against."),
		},
		silent: []written{
			anEdit("he-9019", vocab.StatusProposed, s.auto,
				"Measured only on the held-out split",
				"The held-in split has never been run, so the projection says nothing\n"+
					"about this edit."),
			aRun("he-9019", vocab.SplitHeldOut, vocab.VerdictImprove,
				"A gain on the other split, which this projection does not read."),
		},
	}
}

// validated_out: a held-out run that did not regress.
func validatedOut(s exemplars) corpus {
	return corpus{
		name: vocab.ProjectionValidatedOut.String(),
		fires: []written{
			anEdit("he-9020", vocab.StatusProposed, s.auto,
				"Measured on the held-out split",
				"The split held back from development did not regress."),
			aRun("he-9020", vocab.SplitHeldOut, vocab.VerdictHold,
				"No regression on the tasks the edit was not developed against."),
		},
		silent: []written{
			anEdit("he-9021", vocab.StatusProposed, s.auto,
				"Measured only on the held-in split",
				"A gain on the split the edit was developed against says nothing about\n"+
					"the one held back from it."),
			aRun("he-9021", vocab.SplitHeldIn, vocab.VerdictImprove,
				"A gain on the split this projection does not read."),
		},
	}
}

// improved: some run shows a strict gain.
func improved(s exemplars) corpus {
	return corpus{
		name: vocab.ProjectionImproved.String(),
		fires: []written{
			anEdit("he-9022", vocab.StatusProposed, s.auto,
				"Measured as a gain",
				"A strict gain over the baseline is what improved reads, on either split."),
			aRun("he-9022", vocab.SplitHeldIn, vocab.VerdictImprove,
				"The pass rate is above the baseline the same suite gave without the edit."),
		},
		silent: []written{
			anEdit("he-9023", vocab.StatusProposed, s.auto,
				"Measured as no change",
				"Holding is not improving: an edit that costs nothing and gains nothing\n"+
					"has not shown it is worth keeping."),
			aRun("he-9023", vocab.SplitHeldIn, vocab.VerdictHold,
				"The pass rate matches the baseline exactly."),
		},
	}
}

// effective: what binds. uzushio adds one kind-scoped alternative to the preset
// projection, and this is the pair that shows it.
func effective(s exemplars) corpus {
	return corpus{
		name: config.ProjectionEffective,
		fires: append([]written{
			anEdit("he-9024", vocab.StatusAccepted, s.auto,
				"Accepted, measured, in force and unreplaced",
				"All of what uzushio's alternative asks: accepted, in force today, no\n"+
					"successor, and the three projections a run can make hold."),
		}, validating("he-9024")...),
		silent: []written{
			anEdit("he-9025", vocab.StatusProposed, s.auto,
				"Proposed and nothing more",
				"A proposal binds nothing, whatever else the corpus holds."),
		},
	}
}

// has_inforce_successor: the preset projection that reads a lineage. DocDag
// ships no fixture for it, and uzushio's edits take part in supersedes, so the
// pair is written from edits.
func hasInforceSuccessor(s exemplars) corpus {
	successor := anEdit("he-9027", vocab.StatusAccepted, s.auto,
		"The edit that replaced it",
		"Accepted and in force, which is what makes the edit it supersedes have a\n"+
			"successor rather than merely a declared one.")
	successor.Supersedes = []doc.Supersession{{Edit: "he-9026", Reason: "recurrence"}}

	return corpus{
		name: config.ProjectionInForceSuccessor,
		fires: []written{
			anEdit("he-9026", vocab.StatusSuperseded, s.auto,
				"The edit that was replaced",
				"Superseded, with the replacement in force, so the projection holds here."),
			successor,
		},
		silent: []written{
			anEdit("he-9028", vocab.StatusProposed, s.auto,
				"An edit nobody has replaced",
				"Nothing supersedes it, so there is no successor to be in force."),
		},
	}
}
