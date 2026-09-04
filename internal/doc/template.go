package doc

import (
	"fmt"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// TemplateDay is the day a skeleton carries. It is a placeholder like every
// other value in a template — a day in the past that no period reads as
// meaningful — and it is replaced along with the rest.
const TemplateDay = "2000-01-01"

// Template returns a skeleton document of one kind: every key the kind
// declares, filled with a placeholder the validator accepts, so a person
// starting from it edits values rather than remembering keys. It is what
// `docdag new` would write if it knew uzushio's kinds.
//
// The skeleton is built from the same writers the harness uses, so a key added
// to a kind appears in the template without anyone remembering to add it, and a
// template that stopped being a valid document fails the test that renders it.
func Template(kind vocab.Kind) (string, error) {
	var document Document
	switch kind {
	case vocab.KindEdit:
		document = templateEdit()
	case vocab.KindPattern:
		document = templatePattern()
	case vocab.KindRun:
		document = templateRun()
	case vocab.KindVerifier:
		document = templateVerifier()
	default:
		return "", fmt.Errorf("%w: no template for kind %q", ErrDocument, kind)
	}
	out, err := document.Bytes()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func templateEdit() Edit {
	return Edit{
		EditID:    "he-0000",
		Title:     "What this edit changes, in one sentence",
		Date:      TemplateDay,
		Status:    vocab.StatusProposed,
		Component: "memory",
		Touches:   []string{"memory"},
		RootCause: "Why the failure happened, not what to do about it.",
		Approval:  vocab.ApprovalHuman,
		// approved_by is filled in when the edit is accepted; an unwritten key
		// is the honest state of a proposal.
		About:    []string{"topic/spec-corpus"},
		Predicts: []Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body: "What the edit does to the surface, and what a reader should see\n" +
			"differently once it is in force.",
	}
}

func templatePattern() Pattern {
	return Pattern{
		PatternID: "fp/example",
		Title:     "The failure, named as what goes wrong",
		Date:      TemplateDay,
		Status:    vocab.StatusOpen,
		Category:  vocab.CategoryNotProvided,
		Context:   "The situation the control action is unsafe in.",
		Component: "memory",
		// A CMoA trace run-id, which is what evidence: carries: the trace the
		// failure was seen in, not an evaluation run of uzushio's own.
		Evidence: []string{"20000101T000000Z-0000beef"},
		Body: "How the failure was seen, how often, and what it costs. The CMoA\n" +
			"traces it was seen in belong under evidence:.",
	}
}

func templateRun() Run {
	return Run{
		Edit:      "he-0000",
		Day:       TemplateDay,
		ModelSlug: "model",
		Split:     vocab.SplitHeldOut,
		Title:     "What this run measured",
		Date:      TemplateDay,
		Verdict:   vocab.VerdictInconclusive,
		Suite:     "the task set the split is defined over",
		Trials:    1,
		Validates: []Validation{{Edit: "he-0000", Model: "model", PassRate: 0, BaselinePassRate: 0}},
		Body: "How the run was made: the seed, the harness revision, and anything\n" +
			"about the measurement a reader would need to repeat it.",
	}
}

func templateVerifier() Verifier {
	return Verifier{
		Task:          "example",
		Day:           TemplateDay,
		Title:         "What this check found out about the task's verifier",
		Date:          TemplateDay,
		Verdict:       vocab.HealthInconclusive,
		KillRate:      NoKillRate,
		Mutants:       0,
		ReferenceRuns: 1,
		Report:        "doctor/20000101T000000Z-0000beef/report.json",
		Body: "Which mutants survived, and what that says about what the verifier\n" +
			"is not testing. The numbers are in the report the report: key names.",
	}
}
