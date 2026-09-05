package vault

import (
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/Kaikei-e/DocDag/config"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

func mustConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	return cfg
}

// TestConfigValidates is the whole contract of the package in one line:
// Config already ran cfg.Validate and the two self-checks, so an error here is
// a configuration DocDag would refuse.
func TestConfigValidates(t *testing.T) {
	cfg := mustConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := SelfCheck(cfg); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if cfg.Preset != config.PresetSpec {
		t.Fatalf("preset = %q, want %q: dropping it merges onto the ADR preset", cfg.Preset, config.PresetSpec)
	}
	if cfg.PresetVersion != config.SpecPresetVersion {
		t.Fatalf("preset_version = %d, want %d", cfg.PresetVersion, config.SpecPresetVersion)
	}
	// id_width beside kinds is refused outright by config.Resolve.
	if cfg.IDWidth != 0 {
		t.Fatalf("id_width = %d, want 0", cfg.IDWidth)
	}
	if cfg.Binding != config.ProjectionEffective {
		t.Fatalf("binding = %q, want %q", cfg.Binding, config.ProjectionEffective)
	}
}

func TestKindsAreDeclared(t *testing.T) {
	cfg := mustConfig(t)
	for _, k := range vocab.AllKinds() {
		spec, ok := cfg.Kind(k.String())
		if !ok {
			t.Fatalf("kind %s is not declared", k)
		}
		dir, _ := vocab.Dir(k)
		if spec.Dir != dir {
			t.Fatalf("kind %s dir = %q, want %q", k, spec.Dir, dir)
		}
		// The fixture layer reroots relative kind directories and misreads
		// absolute ones, so the generated file keeps them relative.
		if len(spec.Dir) > 0 && spec.Dir[0] == '/' {
			t.Fatalf("kind %s has an absolute dir %q", k, spec.Dir)
		}
		if !spec.Closed {
			t.Fatalf("kind %s is not closed; an undeclared key should be reported", k)
		}
		if !spec.AppendOnly {
			t.Fatalf("kind %s is not append_only", k)
		}
		want := vocab.Strings(vocab.KindStatuses(k))
		if !slices.Equal(spec.StatusValues, want) {
			t.Fatalf("kind %s status_values = %v, want %v", k, spec.StatusValues, want)
		}
	}
	// A run and a verifier health check are measurements and answer to no
	// status vocabulary.
	for _, k := range []vocab.Kind{vocab.KindRun, vocab.KindVerifier} {
		if spec, _ := cfg.Kind(k.String()); len(spec.StatusValues) != 0 {
			t.Fatalf("%s declares statuses %v", k, spec.StatusValues)
		}
	}
	// An edit and a calibration have lifetimes; a pattern, a run and a
	// verifier check are always in force. The calibration's is the whole of
	// its staleness mechanism: it stops binding on a day rather than when
	// somebody remembers to retire it.
	for _, k := range []vocab.Kind{vocab.KindEdit, vocab.KindCalibration} {
		if _, ok := cfg.KindPeriod(k.String()); !ok {
			t.Fatalf("the %s kind declares no period", k)
		}
	}
	for _, k := range []vocab.Kind{vocab.KindPattern, vocab.KindRun, vocab.KindVerifier} {
		if _, ok := cfg.KindPeriod(k.String()); ok {
			t.Fatalf("kind %s declares a period", k)
		}
	}
}

// TestTouchesIsNotRequired records the trap it avoids: `required` reads a
// scalar, so a required list field reports missing_field on every document
// that writes the list.
func TestTouchesIsNotRequired(t *testing.T) {
	cfg := mustConfig(t)
	for _, name := range []vocab.Field{vocab.FieldTouches, vocab.FieldEvidence} {
		for _, kind := range vocab.AllKinds() {
			spec, ok := cfg.Field(kind.String(), name.String())
			if ok && spec.Required {
				t.Fatalf("%s declares the list field %s required", kind, name)
			}
		}
	}
}

func TestRequiredScalarFields(t *testing.T) {
	cfg := mustConfig(t)
	required := []struct {
		kind  vocab.Kind
		field vocab.Field
	}{
		{vocab.KindEdit, vocab.FieldComponent},
		{vocab.KindEdit, vocab.FieldApproval},
		{vocab.KindPattern, vocab.FieldCategory},
		{vocab.KindPattern, vocab.FieldContext},
		{vocab.KindPattern, vocab.FieldComponent},
		{vocab.KindRun, vocab.FieldSplit},
		{vocab.KindRun, vocab.FieldVerdict},
		{vocab.KindRun, vocab.FieldSuite},
		{vocab.KindVerifier, vocab.FieldVerdict},
		{vocab.KindCalibration, vocab.FieldVerdict},
		{vocab.KindCalibration, vocab.FieldJudge},
		{vocab.KindCalibration, vocab.FieldPool},
		{vocab.KindCalibration, vocab.FieldTieHandling},
	}
	for _, want := range required {
		spec, ok := cfg.Field(want.kind.String(), want.field.String())
		if !ok {
			t.Fatalf("%s does not declare %s", want.kind, want.field)
		}
		if !spec.Required {
			t.Fatalf("%s.%s is not required", want.kind, want.field)
		}
	}
}

func TestComponentVocabularyComesFromCMoA(t *testing.T) {
	cfg := mustConfig(t)
	all, err := surfaces.All()
	if err != nil {
		t.Fatalf("surfaces.All: %v", err)
	}
	for _, kind := range []vocab.Kind{vocab.KindEdit, vocab.KindPattern} {
		spec, ok := cfg.Field(kind.String(), vocab.FieldComponent.String())
		if !ok {
			t.Fatalf("%s declares no component field", kind)
		}
		if !slices.Equal(spec.OneOf, all) {
			t.Fatalf("%s component one_of = %v, want %v", kind, spec.OneOf, all)
		}
	}
}

func TestEdges(t *testing.T) {
	cfg := mustConfig(t)
	edit := vocab.KindEdit.String()
	widened := map[model.EdgeType]struct{ from, to bool }{
		config.EdgeSupersedes:     {from: true, to: true},
		config.EdgePremise:        {from: true},
		config.EdgeCounterexample: {from: true},
		config.EdgeAbout:          {from: true},
	}
	for name, want := range widened {
		spec, ok := cfg.Edge(name)
		if !ok {
			t.Fatalf("edge %s is not declared", name)
		}
		if want.from && !slices.Contains(spec.From, edit) {
			t.Fatalf("edge %s from = %v, want it to admit an edit", name, spec.From)
		}
		if want.to && !slices.Contains(spec.To, edit) {
			t.Fatalf("edge %s to = %v, want it to admit an edit", name, spec.To)
		}
	}

	predicts, ok := cfg.Edge(model.EdgeType(vocab.EdgePredicts.String()))
	if !ok {
		t.Fatal("the predicts edge is not declared")
	}
	if !slices.Equal(predicts.From, []string{edit}) || !slices.Equal(predicts.To, []string{vocab.KindPattern.String()}) {
		t.Fatalf("predicts runs %v -> %v", predicts.From, predicts.To)
	}
	expect, ok := predicts.Attr(vocab.AttrExpect.String())
	if !ok || !expect.Required {
		t.Fatalf("predicts.expect = %+v, want a required attribute", expect)
	}
	if outcome, ok := predicts.Attr(vocab.AttrOutcome.String()); !ok || outcome.Required {
		t.Fatalf("predicts.outcome = %+v, want an optional attribute", outcome)
	}

	validates, ok := cfg.Edge(model.EdgeType(vocab.EdgeValidates.String()))
	if !ok {
		t.Fatal("the validates edge is not declared")
	}
	if !slices.Equal(validates.From, []string{vocab.KindRun.String()}) || !slices.Equal(validates.To, []string{edit}) {
		t.Fatalf("validates runs %v -> %v", validates.From, validates.To)
	}
	for _, name := range []vocab.EdgeAttr{vocab.AttrModel, vocab.AttrPassRate, vocab.AttrBaselinePassRate} {
		attr, ok := validates.Attr(name.String())
		if !ok || !attr.Required {
			t.Fatalf("validates.%s = %+v, want a required attribute", name, attr)
		}
	}
}

// TestHistoryEdgesCarryNoTarget states the rule as a test, because the
// temptation to add one is real and the consequence is unrecoverable.
//
// A target condition is a liveness constraint on the document at the far end,
// and neither edge uzushio declares points at something that has to still be
// current: a run records what was measured on the day it was measured, and a
// prediction records what was claimed when it was claimed. Both are history.
//
// A `leaf_of: supersedes` target on either would turn every historical record
// into a stale_target error the moment the edit it names is superseded — the
// ordinary lifecycle — with no legal move out of it. The record may not be
// retargeted or deleted: its kind is append_only and the workflow refuses the
// diff. stale_target may not be silenced: it is a structural check, and a
// configuration may raise one but never lower it. DocDag's single escape,
// skipping the check where the *declaring* document is out of force
// (internal/graph/target.go, declaredInForce — "a history that cannot be added
// to without breaking the build is a ratchet rather than an archive"), needs a
// period on the declaring kind, and a measurement must not have one: a run
// does not expire.
func TestHistoryEdgesCarryNoTarget(t *testing.T) {
	cfg := mustConfig(t)
	for _, name := range vocab.AllEdges() {
		spec, ok := cfg.Edge(model.EdgeType(name.String()))
		if !ok {
			t.Fatalf("edge %s is not declared", name)
		}
		if spec.Target != nil {
			t.Fatalf("edge %s declares target %+v; a record is not a live pointer", name, spec.Target)
		}
	}
}

func TestProjections(t *testing.T) {
	cfg := mustConfig(t)
	for _, name := range vocab.AllProjections() {
		if _, ok := cfg.Projection(name.String()); !ok {
			t.Fatalf("projection %s is not declared", name)
		}
	}
	effective, ok := cfg.Projection(config.ProjectionEffective)
	if !ok {
		t.Fatal("the effective projection is not declared")
	}
	// The alternative uzushio adds is the last one, and it is scoped to a kind
	// none of the preset's alternatives reach, so what effective says about a
	// clause is untouched.
	last := effective.AnyOf[len(effective.AnyOf)-1].When
	match, ok := last.Attr[config.KeyKind]
	if !ok || match.Eq == nil || *match.Eq != vocab.KindEdit.String() {
		t.Fatalf("the added effective alternative is not scoped to an edit: %+v", last.Attr)
	}
	for _, key := range []string{
		vocab.ProjectionValidatedIn.String(),
		vocab.ProjectionValidatedOut.String(),
		vocab.ProjectionImproved.String(),
		config.AttrInForce,
	} {
		if cond, ok := last.Attr[key]; !ok || cond.Eq == nil || *cond.Eq != config.ProjectionTrue {
			t.Fatalf("the added effective alternative does not require %s to be true", key)
		}
	}
}

func TestRules(t *testing.T) {
	cfg := mustConfig(t)
	byName := map[string]config.Rule{}
	for _, rule := range cfg.Rules {
		byName[rule.Name] = rule
	}
	for _, name := range vocab.AllRules() {
		rule, ok := byName[name.String()]
		if !ok {
			t.Fatalf("rule %s is not declared", name)
		}
		if rule.Message == "" {
			t.Fatalf("rule %s has no message", name)
		}
		// A document's kind is the directory's answer, and every uzushio rule
		// is about one kind's documents. A rule that forgets the clause is a
		// rule about the whole vault.
		if match, ok := rule.When.Attr[config.KeyKind]; !ok || match.Eq == nil {
			t.Fatalf("rule %s does not open with a kind clause", name)
		}
	}
	severities := map[vocab.Rule]model.Severity{
		vocab.RuleAcceptedUnvalidated:     model.SeverityError,
		vocab.RuleEditTouchesReadonly:     model.SeverityError,
		vocab.RuleEditWithoutPrediction:   model.SeverityError,
		vocab.RuleRejectedWithoutRun:      model.SeverityError,
		vocab.RuleProposeOnlyAccepted:     model.SeverityError,
		vocab.RuleAcceptedWithoutApprover: model.SeverityError,
		vocab.RulePatternResolvedUnfixed:  model.SeverityWarn,
		vocab.RulePredictsWithdrawn:       model.SeverityWarn,
		vocab.RuleEditWithoutTopic:        model.SeverityError,
	}
	for name, want := range severities {
		if got := byName[name.String()].Severity; got != want {
			t.Fatalf("rule %s severity = %q, want %q", name, got, want)
		}
	}
	// The two rules whose subject is an edge the document declares are guarded
	// with in_force: an out-of-force document's edges are dropped from the
	// index, so a scheduled edit would otherwise be reported for declaring
	// nothing.
	for _, name := range []vocab.Rule{vocab.RuleEditWithoutPrediction, vocab.RuleEditWithoutTopic} {
		cond, ok := byName[name.String()].When.Attr[config.AttrInForce]
		if !ok || cond.Eq == nil || *cond.Eq != config.ProjectionTrue {
			t.Fatalf("rule %s is not guarded with in_force", name)
		}
	}
}

// TestProposeOnlyAndApprovalRulesFollowCMoA holds the two autonomy rules to the
// vocabulary CMoA publishes rather than to a list written here.
func TestProposeOnlyAndApprovalRulesFollowCMoA(t *testing.T) {
	cfg := mustConfig(t)
	components := func(rule config.Rule) []string {
		var out []string
		for _, alternative := range rule.When.AnyOf {
			if cond, ok := alternative.Attr[vocab.FieldComponent.String()]; ok && cond.Eq != nil {
				out = append(out, *cond.Eq)
			}
		}
		slices.Sort(out)
		return out
	}
	find := func(name vocab.Rule) config.Rule {
		index := slices.IndexFunc(cfg.Rules, func(r config.Rule) bool { return r.Name == name.String() })
		if index < 0 {
			t.Fatalf("rule %s is not declared", name)
		}
		return cfg.Rules[index]
	}
	propose, err := surfaces.ProposeOnly()
	if err != nil {
		t.Fatalf("surfaces.ProposeOnly: %v", err)
	}
	slices.Sort(propose)
	if got := components(find(vocab.RuleProposeOnlyAccepted)); !slices.Equal(got, propose) {
		t.Fatalf("propose_only_accepted covers %v, want %v", got, propose)
	}
	human, err := surfaces.HumanApproval()
	if err != nil {
		t.Fatalf("surfaces.HumanApproval: %v", err)
	}
	slices.Sort(human)
	if got := components(find(vocab.RuleAcceptedWithoutApprover)); !slices.Equal(got, human) {
		t.Fatalf("accepted_without_approver covers %v, want %v", got, human)
	}
}

// TestReferencePatternCoversEveryKind guards the failure mode that is silence:
// a wikilink the reference pattern rejects is dropped without a finding, so a
// kind missing from the pattern is a kind whose links are never checked.
func TestReferencePatternCoversEveryKind(t *testing.T) {
	cfg := mustConfig(t)
	shape, err := regexp.Compile(cfg.References.Pattern)
	if err != nil {
		t.Fatalf("references.pattern does not compile: %v", err)
	}
	accepted := []string{
		"UZ-V-001", "conform/uz-c-001", "dev-0001", "interp/UZ-V-001@2026-09-05",
		"premise/x", "principle/x", "pm-0001", "topic/x",
		"he-0001", "fp/retry-storm", "run/he-0001@2026-09-05-gemma-3-12b-out",
		"run/he-0001@2026-09-05-m-in-2",
		"verifier/hello@2026-09-05", "verifier/task-hello@2026-09-05-2",
	}
	for _, token := range accepted {
		if !shape.MatchString(token) {
			t.Fatalf("references.pattern rejects %q", token)
		}
	}
	for _, token := range []string{
		"", "not-an-id", "he-1", "fp/Retry", "run/he-0001", "verifier/hello", "verifier/Hello@2026-09-05",
	} {
		if shape.MatchString(token) {
			t.Fatalf("references.pattern accepts %q", token)
		}
	}
	if cfg.References.Dangling != string(model.SeverityError) {
		t.Fatalf("references.dangling = %q, want error", cfg.References.Dangling)
	}
	if !cfg.Scans(config.ScanBody) || !cfg.Scans(config.ScanFrontmatter) {
		t.Fatalf("references.scan = %v, want body and frontmatter", cfg.References.Scan)
	}
	if cfg.Structural[model.RuleMissingFrontmatter] != model.SeverityError {
		t.Fatalf("structural = %v, want missing_frontmatter raised to error", cfg.Structural)
	}
}

func TestSelfCheckRejectsDuplicateRuleNames(t *testing.T) {
	cfg := mustConfig(t)
	cfg.Rules = append(cfg.Rules, cfg.Rules[len(cfg.Rules)-1])
	err := SelfCheck(cfg)
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("SelfCheck error = %v, want ErrConfig", err)
	}
	// Validate is happy with it, which is the reason SelfCheck exists.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected the duplicate after all: %v", err)
	}
}

func TestSelfCheckRejectsAnUnknownAttributeKey(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*config.Config)
	}{
		{
			name: "a misspelled projection in a rule",
			mut: func(cfg *config.Config) {
				cfg.Rules = append(cfg.Rules, config.Rule{
					Name:     "probe",
					Severity: model.SeverityError,
					When: config.Condition{Attr: map[string]config.AttrCondition{
						config.KeyKind: eq(vocab.KindEdit.String()),
						"validated_ni": isTrue(),
					}},
					Message: "probe",
				})
			},
		},
		{
			name: "a field of another kind",
			mut: func(cfg *config.Config) {
				cfg.Rules = append(cfg.Rules, config.Rule{
					Name:     "probe",
					Severity: model.SeverityError,
					When: config.Condition{Attr: map[string]config.AttrCondition{
						config.KeyKind: eq(vocab.KindRun.String()),
						// component belongs to an edit and a pattern.
						vocab.FieldComponent.String(): eq("memory"),
					}},
					Message: "probe",
				})
			},
		},
		{
			name: "an unknown key inside a one-hop clause",
			mut: func(cfg *config.Config) {
				cfg.Rules = append(cfg.Rules, config.Rule{
					Name:     "probe",
					Severity: model.SeverityError,
					When: config.Condition{
						Attr: map[string]config.AttrCondition{config.KeyKind: eq(vocab.KindEdit.String())},
						Via: &config.ViaCondition{
							Edge: vocab.EdgePredicts.String(),
							Attr: map[string]config.AttrCondition{"verdict": eq("improve")},
						},
					},
					Message: "probe",
				})
			},
		},
		{
			name: "a misspelled projection in a projection",
			mut: func(cfg *config.Config) {
				cfg.Projections = append(cfg.Projections, config.ProjectionSpec{
					Name: "probe",
					When: config.Condition{Attr: map[string]config.AttrCondition{"improvd": isTrue()}},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustConfig(t)
			tt.mut(&cfg)
			if err := SelfCheck(cfg); !errors.Is(err, ErrConfig) {
				t.Fatalf("SelfCheck error = %v, want ErrConfig", err)
			}
		})
	}
}

// TestSelfCheckAdmitsAKindScopedField is the other half: narrowing a rule to a
// kind lets it read that kind's fields, which is what every uzushio rule does.
func TestSelfCheckAdmitsAKindScopedField(t *testing.T) {
	cfg := mustConfig(t)
	cfg.Rules = append(cfg.Rules, config.Rule{
		Name:     "probe",
		Severity: model.SeverityError,
		When: config.Condition{Attr: map[string]config.AttrCondition{
			config.KeyKind:              eq(vocab.KindRun.String()),
			vocab.FieldVerdict.String(): eq(vocab.VerdictRegress.String()),
		}},
		Message: "probe",
	})
	if err := SelfCheck(cfg); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestVerifierKind pins the shape of the kind `uzushio task doctor` writes: a
// verdict out of the health vocabulary, the numbers as unconstrained scalars,
// and no edge and no rule of its own. The last is the part worth stating: a
// health check points at nothing in this vault — the task it is about is
// CMoA's — so an edge would have no far end to declare.
func TestVerifierKind(t *testing.T) {
	cfg := mustConfig(t)
	spec, ok := cfg.Kind(vocab.KindVerifier.String())
	if !ok {
		t.Fatal("the verifier kind is not declared")
	}
	if spec.ID != vocab.VerifierIDPattern {
		t.Fatalf("verifier id = %q, want %q", spec.ID, vocab.VerifierIDPattern)
	}
	verdict, ok := cfg.Field(vocab.KindVerifier.String(), vocab.FieldVerdict.String())
	if !ok {
		t.Fatal("the verifier kind declares no verdict")
	}
	if !slices.Equal(verdict.OneOf, vocab.Strings(vocab.AllHealths())) {
		t.Fatalf("verifier verdict one_of = %v, want %v", verdict.OneOf, vocab.Strings(vocab.AllHealths()))
	}
	// A run's verdict vocabulary and a verifier's share a key and share nothing
	// else. Declaring either vocabulary on the other kind is the mistake this
	// checks for.
	runVerdict, ok := cfg.Field(vocab.KindRun.String(), vocab.FieldVerdict.String())
	if !ok {
		t.Fatal("the run kind declares no verdict")
	}
	for _, word := range runVerdict.OneOf {
		if word != vocab.VerdictInconclusive.String() && slices.Contains(verdict.OneOf, word) {
			t.Fatalf("the verifier verdict admits %q, which is a run's word", word)
		}
	}
	// Every key the writer emits has to be declared: the kind is closed, so an
	// undeclared key is an undeclared_field error on every document.
	for _, field := range vocab.VerifierFields() {
		if _, ok := cfg.Field(vocab.KindVerifier.String(), field.String()); !ok {
			t.Fatalf("the verifier kind does not declare %s", field)
		}
	}
	// No edge admits a verifier at either end, and no rule is about one.
	for _, edge := range cfg.Edges {
		if slices.Contains(edge.From, vocab.KindVerifier.String()) ||
			slices.Contains(edge.To, vocab.KindVerifier.String()) {
			t.Fatalf("edge %s admits a verifier; this step declares none", edge.Name)
		}
	}
	for _, rule := range cfg.Rules {
		if match, ok := rule.When.Attr[config.KeyKind]; ok && match.Eq != nil &&
			*match.Eq == vocab.KindVerifier.String() {
			t.Fatalf("rule %s is about a verifier; this step declares none, so it would need a fixture", rule.Name)
		}
	}
}

// TestCalibrationKind pins the shape of the kind `uzushio judge calibrate`
// writes, and the one thing about it that is not like the verifier's: it has a
// period, and being in force is what makes it binding.
func TestCalibrationKind(t *testing.T) {
	cfg := mustConfig(t)
	spec, ok := cfg.Kind(vocab.KindCalibration.String())
	if !ok {
		t.Fatal("the calibration kind is not declared")
	}
	if spec.ID != vocab.CalibrationIDPattern {
		t.Fatalf("calibration id = %q, want %q", spec.ID, vocab.CalibrationIDPattern)
	}
	period, ok := cfg.KindPeriod(vocab.KindCalibration.String())
	if !ok {
		t.Fatal("the calibration kind declares no period; nothing would ever expire")
	}
	if period.Until != vocab.FieldInForceUntil.String() {
		t.Fatalf("calibration period until = %q", period.Until)
	}
	verdict, ok := cfg.Field(vocab.KindCalibration.String(), vocab.FieldVerdict.String())
	if !ok {
		t.Fatal("the calibration kind declares no verdict")
	}
	if !slices.Equal(verdict.OneOf, vocab.Strings(vocab.AllCalibrateds())) {
		t.Fatalf("calibration verdict one_of = %v", verdict.OneOf)
	}
	// The three kinds that write `verdict` write three different vocabularies
	// into it. A word admitted by two of them would make a reader guess which
	// kind wrote the document.
	health, _ := cfg.Field(vocab.KindVerifier.String(), vocab.FieldVerdict.String())
	for _, word := range verdict.OneOf {
		if slices.Contains(health.OneOf, word) {
			t.Fatalf("the calibration verdict admits %q, which is a verifier's word", word)
		}
	}
	handling, ok := cfg.Field(vocab.KindCalibration.String(), vocab.FieldTieHandling.String())
	if !ok || !handling.Required {
		t.Fatal("tie_handling is not required; a coefficient without one cannot be compared")
	}
	for _, field := range vocab.CalibrationFields() {
		if _, ok := cfg.Field(vocab.KindCalibration.String(), field.String()); !ok {
			t.Fatalf("the calibration kind does not declare %s", field)
		}
	}
	// A calibration is binding while it is in force, which is the alternative
	// the effective projection has to carry for it.
	index := slices.IndexFunc(cfg.Projections, func(spec config.ProjectionSpec) bool {
		return spec.Name == config.ProjectionEffective
	})
	if index < 0 {
		t.Fatal("no effective projection")
	}
	found := false
	for _, alternative := range cfg.Projections[index].AnyOf {
		kind, ok := alternative.When.Attr[config.KeyKind]
		if ok && kind.Eq != nil && *kind.Eq == vocab.KindCalibration.String() {
			found = true
		}
	}
	if !found {
		t.Fatal("nothing makes a calibration binding; judge status would always report none")
	}
	// A calibration takes part in exactly one edge, and it is the one that
	// retires the previous measurement of the same judge. Any other would be
	// a relation nothing writes.
	for _, edge := range cfg.Edges {
		touches := slices.Contains(edge.From, vocab.KindCalibration.String()) ||
			slices.Contains(edge.To, vocab.KindCalibration.String())
		if touches && edge.Name != config.EdgeSupersedes.String() {
			t.Fatalf("edge %s admits a calibration; only supersedes should", edge.Name)
		}
	}
	supersedes := slices.IndexFunc(cfg.Edges, func(spec config.EdgeSpec) bool {
		return spec.Name == config.EdgeSupersedes.String()
	})
	if supersedes < 0 {
		t.Fatal("no supersedes edge")
	}
	for _, end := range [][]string{cfg.Edges[supersedes].From, cfg.Edges[supersedes].To} {
		if !slices.Contains(end, vocab.KindCalibration.String()) {
			t.Fatal("a calibration cannot supersede a calibration; a worse measurement " +
				"would never retire a better one")
		}
	}
	// The calibration's effective alternative has to refuse a document a
	// newer measurement replaced, or a judge measured `uncalibrated` today
	// leaves last month's `calibrated` binding for the rest of its period.
	retired := false
	for _, alternative := range cfg.Projections[index].AnyOf {
		kind, ok := alternative.When.Attr[config.KeyKind]
		if !ok || kind.Eq == nil || *kind.Eq != vocab.KindCalibration.String() {
			continue
		}
		not := alternative.When.Not
		if not == nil || not.ViaInbound == nil {
			continue
		}
		if not.ViaInbound.Edge != config.EdgeSupersedes.String() {
			continue
		}
		successor, ok := not.ViaInbound.Attr[config.KeyKind]
		if !ok || successor.Eq == nil || *successor.Eq != vocab.KindCalibration.String() {
			continue
		}
		if force, ok := not.ViaInbound.Attr[config.AttrInForce]; ok &&
			force.Eq != nil && *force.Eq == config.ProjectionTrue {
			retired = true
		}
	}
	if !retired {
		t.Fatal("a calibration keeps binding after a newer one replaces it")
	}
	// And the word a re-measurement gives as its reason has to be in the
	// vocabulary, or every superseding calibration is an edge_attr_invalid.
	reason, ok := cfg.Edges[supersedes].Attrs[config.AttrReason]
	if !ok {
		t.Fatal("the supersedes edge declares no reason")
	}
	if !slices.Contains(reason.OneOf, vocab.ReasonRemeasured) {
		t.Fatalf("supersedes reason one_of = %v, which has no word for a re-measurement",
			reason.OneOf)
	}
	for _, rule := range cfg.Rules {
		if match, ok := rule.When.Attr[config.KeyKind]; ok && match.Eq != nil &&
			*match.Eq == vocab.KindCalibration.String() {
			t.Fatalf("rule %s is about a calibration; this step declares none, so it would need a fixture", rule.Name)
		}
	}
}
