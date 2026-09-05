// Package vocab is the one place uzushio spells its own words. Every string
// that appears both in the generated docdag.yaml and in a document uzushio
// writes — a kind name, a directory, a status, a field, an edge, a projection,
// a rule name, a value out of a closed vocabulary — is declared here and
// nowhere else, so a rename is one edit and a typo is a compile error rather
// than a rule that quietly fires on nothing.
//
// The package deliberately imports nothing from DocDag: it is the vocabulary,
// not the schema. internal/vault turns these words into a DocDag
// configuration; internal/doc will turn them into frontmatter.
package vocab

import "slices"

// Kind is a uzushio document kind. The four kinds are the harness's own
// records: the edit a proposer wants to make, the failure pattern an edit
// answers, the evaluation run that settles whether it worked, and the verifier
// health check that says whether the run's answer was worth anything.
type Kind string

// The kinds uzushio adds to the spec preset.
const (
	// KindEdit is a proposed change to one harness surface.
	KindEdit Kind = "edit"
	// KindPattern is a recurring failure, written as an STPA unsafe control
	// action: a component, a context, and the way the control went wrong.
	KindPattern Kind = "pattern"
	// KindRun is one evaluation of one edit on one split, written by the
	// harness rather than by a person.
	KindRun Kind = "run"
	// KindVerifier is one health check of one task's verifier: what it did to
	// a reference solution it should accept, and to the mutants it should
	// reject. It is written by `uzushio task doctor` and never by a person.
	KindVerifier Kind = "verifier"
)

// String returns the kind's name as the configuration writes it.
func (k Kind) String() string { return string(k) }

// AllKinds returns the uzushio kinds, sorted by name so a generated
// configuration and a generated listing agree on order.
func AllKinds() []Kind { return []Kind{KindEdit, KindPattern, KindRun, KindVerifier} }

// Kind directories, relative to the vault root. They stay relative: DocDag's
// fixture layer reroots relative kind directories and mis-reads absolute ones.
const (
	DirEdits     = "spec/edits"
	DirPatterns  = "spec/patterns"
	DirRuns      = "spec/runs"
	DirVerifiers = "spec/verifiers"
)

// Dir returns the directory a kind's documents live in.
func Dir(k Kind) (string, bool) {
	switch k {
	case KindEdit:
		return DirEdits, true
	case KindPattern:
		return DirPatterns, true
	case KindRun:
		return DirRuns, true
	case KindVerifier:
		return DirVerifiers, true
	}
	return "", false
}

// Status is a value of a kind's status vocabulary. The edit and pattern
// vocabularies are disjoint apart from withdrawn, which means the same thing
// in both: the document was taken back rather than decided.
type Status string

// The edit vocabulary.
const (
	StatusProposed   Status = "proposed"
	StatusAccepted   Status = "accepted"
	StatusRejected   Status = "rejected"
	StatusSuperseded Status = "superseded"
	StatusWithdrawn  Status = "withdrawn"
)

// The pattern vocabulary. A pattern is open until an edit has fixed it, and
// withdrawn where it turned out not to be a pattern at all.
const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

// String returns the status as frontmatter writes it.
func (s Status) String() string { return string(s) }

// EditStatuses is the status vocabulary of an edit, in lifecycle order.
func EditStatuses() []Status {
	return []Status{StatusProposed, StatusAccepted, StatusRejected, StatusSuperseded, StatusWithdrawn}
}

// PatternStatuses is the status vocabulary of a pattern, in lifecycle order.
func PatternStatuses() []Status {
	return []Status{StatusOpen, StatusResolved, StatusWithdrawn}
}

// A run has no status: it is a measurement, not a decision, and there is
// nothing about a measurement to accept or withdraw. KindStatuses says so by
// returning nothing for it, which is what "this kind has no status" has to be
// written as for DocDag to leave it unchecked.
// The same is true of a verifier health check.
func KindStatuses(k Kind) []Status {
	switch k {
	case KindEdit:
		return EditStatuses()
	case KindPattern:
		return PatternStatuses()
	case KindRun, KindVerifier:
		return nil
	}
	return nil
}

// Category is an STPA unsafe control action type. The four are the whole of
// the classification: a control action is unsafe because it was not given,
// because giving it was itself unsafe, because it came at the wrong moment, or
// because it was applied for the wrong length of time.
type Category string

// The four unsafe control action types.
const (
	CategoryNotProvided    Category = "not-provided"
	CategoryUnsafeProvided Category = "unsafe-provided"
	CategoryWrongTiming    Category = "wrong-timing"
	CategoryWrongDuration  Category = "wrong-duration"
)

// String returns the category as frontmatter writes it.
func (c Category) String() string { return string(c) }

// AllCategories returns the four unsafe control action types.
func AllCategories() []Category {
	return []Category{CategoryNotProvided, CategoryUnsafeProvided, CategoryWrongTiming, CategoryWrongDuration}
}

// Verdict is what a run says about the edit it evaluated. uzushio decides the
// word; DocDag only reads it.
type Verdict string

// The verdicts.
const (
	// VerdictImprove is a strict gain over the baseline on this split.
	VerdictImprove Verdict = "improve"
	// VerdictHold is no regression and no gain.
	VerdictHold Verdict = "hold"
	// VerdictRegress is worse than the baseline.
	VerdictRegress Verdict = "regress"
	// VerdictInconclusive is a sequential test that has not finished.
	VerdictInconclusive Verdict = "inconclusive"
)

// String returns the verdict as frontmatter writes it.
func (v Verdict) String() string { return string(v) }

// AllVerdicts returns the four verdicts, best first.
func AllVerdicts() []Verdict {
	return []Verdict{VerdictImprove, VerdictHold, VerdictRegress, VerdictInconclusive}
}

// Health is what a verifier health check says about a task's verifier. It is
// spelled into the same `verdict` key a run writes, because a reader asking
// "what did this document conclude" should not have to know which kind wrote
// it — but it is a vocabulary of its own, and a separate Go type, because a
// verifier is never `improve` and a run is never `healthy`.
type Health string

// The health words.
const (
	// HealthHealthy is a verifier that accepted the reference solution every
	// time and killed the mutants it was supposed to kill.
	HealthHealthy Health = "healthy"
	// HealthUnhealthy is a verifier that rejected the reference solution, or
	// let a hand-written mutant through, or killed too few of them.
	HealthUnhealthy Health = "unhealthy"
	// HealthInconclusive is a check that did not get an answer: a run that
	// timed out, a runner that failed, a mutant that would not apply, or a task
	// with no mutant to measure a rate over.
	HealthInconclusive Health = "inconclusive"
)

// String returns the health word as frontmatter writes it.
func (h Health) String() string { return string(h) }

// AllHealths returns the three health words, best first.
func AllHealths() []Health {
	return []Health{HealthHealthy, HealthUnhealthy, HealthInconclusive}
}

// Split names the half of the task suite a run was measured on. An edit is
// accepted only where both halves agree, which is the whole point of keeping
// them apart.
type Split string

// The two splits.
const (
	// SplitHeldIn is the tasks the edit was developed against.
	SplitHeldIn Split = "held-in"
	// SplitHeldOut is the tasks held back from development.
	SplitHeldOut Split = "held-out"
)

// String returns the split as frontmatter writes it.
func (s Split) String() string { return string(s) }

// AllSplits returns the two splits.
func AllSplits() []Split { return []Split{SplitHeldIn, SplitHeldOut} }

// Expect is what an edit claims about the pattern it predicts.
type Expect string

// The predictions an edit may make.
const (
	// ExpectFix says the edit removes the pattern.
	ExpectFix Expect = "fix"
	// ExpectAtRisk says the edit may make the pattern more likely.
	ExpectAtRisk Expect = "at-risk"
)

// String returns the prediction as the edge attribute writes it.
func (e Expect) String() string { return string(e) }

// AllExpects returns the predictions an edit may make.
func AllExpects() []Expect { return []Expect{ExpectFix, ExpectAtRisk} }

// Outcome is how a prediction turned out. It is written later than the
// prediction it settles, which is why it is optional on the edge.
type Outcome string

// The outcomes of a prediction.
const (
	OutcomeConfirmed Outcome = "confirmed"
	OutcomeRefuted   Outcome = "refuted"
)

// String returns the outcome as the edge attribute writes it.
func (o Outcome) String() string { return string(o) }

// AllOutcomes returns the outcomes of a prediction.
func AllOutcomes() []Outcome { return []Outcome{OutcomeConfirmed, OutcomeRefuted} }

// Approval says whether a person stood behind an edit's acceptance. It is a
// closed scalar vocabulary rather than a free-text name because DocDag can
// require a scalar field and compare it, and cannot test a free-text field for
// presence at all — the approver's name lives beside it in approved_by.
type Approval string

// The approval words.
const (
	// ApprovalHuman says a person accepted the edit.
	ApprovalHuman Approval = "human"
	// ApprovalAuto says the harness accepted it without asking anyone.
	ApprovalAuto Approval = "auto"
)

// String returns the approval word as frontmatter writes it.
func (a Approval) String() string { return string(a) }

// AllApprovals returns the approval words.
func AllApprovals() []Approval { return []Approval{ApprovalHuman, ApprovalAuto} }

// Field is a frontmatter key one of the uzushio kinds declares. The engine
// keys — id, kind, title, date, status — are DocDag's and are not repeated
// here.
type Field string

// Fields of the edit kind.
const (
	// FieldComponent is the one harness surface a document is about. Both the
	// edit and the pattern kinds write it, out of the surfaces vocabulary.
	FieldComponent Field = "component"
	// FieldTouches lists every surface the edit changes. It is a list, so it
	// is never declared required: a list value reads as absent to DocDag's
	// scalar required check. The edit_touches_readonly rule carries the
	// obligation instead.
	FieldTouches Field = "touches"
	// FieldPaths lists the files under the rendered harness directory the
	// edit owns. It is a list, so like touches it is never declared required:
	// a list value reads as absent to DocDag's scalar required check, and
	// DocDag has no path arithmetic to check the entries against the
	// component either. The obligation is uzushio's — `uzushio harness
	// render` and `uzushio run` refuse an edit that writes no paths, and
	// refuse one whose paths do not map onto its component and its touches.
	FieldPaths Field = "paths"
	// FieldDiffSHA256 is the SHA-256 of the sidecar unified diff a
	// system-prompt edit carries, as 64 lowercase hexadecimal digits. It is
	// the tripwire on the sidecar: DocDag parses only .md and its append-only
	// check skips everything else, so a rewritten .diff is invisible to the
	// vault — but rewriting it forces this key to change, and that is an
	// immutable_violation on an accepted edit.
	FieldDiffSHA256 Field = "diff_sha256"
	// FieldRootCause is prose: why the pattern happened, not what to do.
	FieldRootCause Field = "root_cause"
	// FieldApprovedBy names the person who approved the edit.
	FieldApprovedBy Field = "approved_by"
	// FieldApproval is the closed companion of approved_by.
	FieldApproval Field = "approval"
	// FieldInForceFrom and FieldInForceUntil are the days an edit is in force
	// between. They are spelled as the spec preset spells them for clauses, so
	// one reader answers both.
	FieldInForceFrom  Field = "in_force_from"
	FieldInForceUntil Field = "in_force_until"
)

// Fields of the pattern kind.
const (
	// FieldCategory is the STPA unsafe control action type.
	FieldCategory Field = "category"
	// FieldContext is the situation the control action was unsafe in. STPA
	// asks every unsafe control action to state one, so it is required.
	FieldContext Field = "context"
	// FieldEvidence lists the run identifiers the pattern was seen in.
	FieldEvidence Field = "evidence"
)

// Fields of the run kind.
const (
	// FieldSplit is the half of the suite the run measured.
	FieldSplit Field = "split"
	// FieldVerdict is what the run concluded.
	FieldVerdict Field = "verdict"
	// FieldSuite names the task set the split is defined over. Without it a
	// pass rate is a number about nothing.
	FieldSuite Field = "suite"
	// FieldTrials is how many attempts the rate was measured over.
	FieldTrials Field = "trials"
	// FieldTrace is the path to the recorded transcript.
	FieldTrace Field = "trace"
)

// Fields of the verifier kind. The three counts and the rate are written as
// strings rather than as numbers: DocDag compares a scalar field as text, and a
// rate that reads back as 0.8300000000000001 because it went through a float is
// a value no `one_of` and no eye can match. The document records what the check
// measured; the report the `report` key names holds the numbers a machine reads.
const (
	// FieldKillRate is the share of the mutants expected to be killed that were
	// killed, as a decimal string like "0.83".
	FieldKillRate Field = "kill_rate"
	// FieldMutants is how many mutants the check ran, as a decimal string.
	FieldMutants Field = "mutants"
	// FieldReferenceRuns is how many times the reference solution was verified,
	// as a decimal string.
	FieldReferenceRuns Field = "reference_runs"
	// FieldReport is the path to the report.json the check wrote, relative to
	// the task directory.
	FieldReport Field = "report"
)

// String returns the field as frontmatter writes it.
func (f Field) String() string { return string(f) }

// EditFields returns the frontmatter keys an edit declares, sorted.
func EditFields() []Field {
	return sortedFields([]Field{
		FieldComponent, FieldTouches, FieldPaths, FieldDiffSHA256,
		FieldRootCause, FieldApprovedBy,
		FieldApproval, FieldInForceFrom, FieldInForceUntil,
	})
}

// PatternFields returns the frontmatter keys a pattern declares, sorted.
func PatternFields() []Field {
	return sortedFields([]Field{FieldCategory, FieldContext, FieldComponent, FieldEvidence})
}

// RunFields returns the frontmatter keys a run declares, sorted.
func RunFields() []Field {
	return sortedFields([]Field{FieldSplit, FieldVerdict, FieldSuite, FieldTrials, FieldTrace})
}

// VerifierFields returns the frontmatter keys a verifier health check
// declares, sorted.
func VerifierFields() []Field {
	return sortedFields([]Field{
		FieldVerdict, FieldKillRate, FieldMutants, FieldReferenceRuns, FieldReport,
	})
}

// KindFields returns the frontmatter keys one kind declares.
func KindFields(k Kind) []Field {
	switch k {
	case KindEdit:
		return EditFields()
	case KindPattern:
		return PatternFields()
	case KindRun:
		return RunFields()
	case KindVerifier:
		return VerifierFields()
	}
	return nil
}

func sortedFields(fields []Field) []Field {
	slices.Sort(fields)
	return fields
}

// Edge is a typed constraint edge uzushio adds.
type Edge string

// The edges uzushio adds to the spec preset.
const (
	// EdgePredicts runs from an edit to the pattern it claims to move. It is
	// the falsifiable half of a proposal: an edit that predicts nothing cannot
	// be wrong about anything.
	EdgePredicts Edge = "predicts"
	// EdgeValidates runs from a run to the edit it measured.
	EdgeValidates Edge = "validates"
)

// String returns the edge name as the configuration and the frontmatter key
// both write it.
func (e Edge) String() string { return string(e) }

// AllEdges returns the edges uzushio adds.
func AllEdges() []Edge { return []Edge{EdgePredicts, EdgeValidates} }

// EdgeAttr is an attribute one of uzushio's edges carries.
type EdgeAttr string

// Attributes of the predicts edge.
const (
	// AttrExpect is the claim, written when the prediction is made.
	AttrExpect EdgeAttr = "expect"
	// AttrOutcome is how it turned out, written when it is settled.
	AttrOutcome EdgeAttr = "outcome"
)

// Attributes of the validates edge.
const (
	// AttrModel names the model the run was measured on. Spelled as the spec
	// preset's measures edge spells it.
	AttrModel EdgeAttr = "model"
	// AttrPassRate is the edit's pass rate on the split.
	AttrPassRate EdgeAttr = "pass_rate"
	// AttrBaselinePassRate is the pass rate the same suite gave without the
	// edit. Both are recorded because a rate without its baseline says
	// nothing about a change.
	AttrBaselinePassRate EdgeAttr = "baseline_pass_rate"
)

// String returns the attribute name as an edge entry writes it.
func (a EdgeAttr) String() string { return string(a) }

// Projection is a derived boolean attribute uzushio adds.
type Projection string

// The projections uzushio adds.
const (
	// ProjectionValidatedIn holds where a held-in run did not regress.
	ProjectionValidatedIn Projection = "validated_in"
	// ProjectionValidatedOut holds where a held-out run did not regress.
	ProjectionValidatedOut Projection = "validated_out"
	// ProjectionImproved holds where some run showed a strict gain.
	ProjectionImproved Projection = "improved"
)

// String returns the projection name as an attribute condition reads it.
func (p Projection) String() string { return string(p) }

// AllProjections returns the projections uzushio adds.
func AllProjections() []Projection {
	return []Projection{ProjectionValidatedIn, ProjectionValidatedOut, ProjectionImproved}
}

// Rule is the name of a finding uzushio's configuration reports. The name is
// the contract: it is what a suppression, a fixture directory and a report line
// all agree on.
type Rule string

// The rules uzushio adds.
const (
	// RuleAcceptedUnvalidated reports an accepted edit that is missing one of
	// the three things acceptance requires.
	RuleAcceptedUnvalidated Rule = "accepted_unvalidated"
	// RuleEditTouchesReadonly reports an edit that touches something outside
	// the seven surfaces, the read-only components included.
	RuleEditTouchesReadonly Rule = "edit_touches_readonly"
	// RuleEditWithoutPrediction reports an edit that claims nothing.
	RuleEditWithoutPrediction Rule = "edit_without_prediction"
	// RuleRejectedWithoutRun reports a rejection with no evidence behind it.
	RuleRejectedWithoutRun Rule = "rejected_without_run"
	// RuleProposeOnlyAccepted reports an accepted edit to a surface whose
	// edits may only ever be proposed.
	RuleProposeOnlyAccepted Rule = "propose_only_accepted"
	// RuleAcceptedWithoutApprover reports an edit to a human-approval surface
	// that was accepted without a person.
	RuleAcceptedWithoutApprover Rule = "accepted_without_approver"
	// RulePatternResolvedUnfixed reports a pattern called resolved that no
	// accepted edit predicted.
	RulePatternResolvedUnfixed Rule = "pattern_resolved_unfixed"
	// RulePredictsWithdrawn reports a live edit aimed at a withdrawn pattern.
	RulePredictsWithdrawn Rule = "predicts_withdrawn"
	// RuleEditWithoutTopic reports an edit that names no topic.
	RuleEditWithoutTopic Rule = "edit_without_topic"
)

// String returns the rule name as a finding and a fixture directory write it.
func (r Rule) String() string { return string(r) }

// AllRules returns the rules uzushio adds, in the order the configuration
// declares them.
func AllRules() []Rule {
	return []Rule{
		RuleAcceptedUnvalidated,
		RuleEditTouchesReadonly,
		RuleEditWithoutPrediction,
		RuleRejectedWithoutRun,
		RuleProposeOnlyAccepted,
		RuleAcceptedWithoutApprover,
		RulePatternResolvedUnfixed,
		RulePredictsWithdrawn,
		RuleEditWithoutTopic,
	}
}

// Strings converts a vocabulary slice to the plain strings a DocDag
// configuration and a YAML frontmatter both take.
func Strings[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

// Valid reports whether a value belongs to a vocabulary.
func Valid[T ~string](value T, vocabulary []T) bool {
	return slices.Contains(vocabulary, value)
}
