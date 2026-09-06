package judge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// trialQualitySet writes one named trial set. The ordinary fixture helper
// makes D alone, while these quality tests need D, R, or H deliberately.
func trialQualitySet(t *testing.T, dir, name, set string, ids []string) string {
	t.Helper()
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{
			"id": id, "strata": map[string]string{"category": "writing"},
		})
	}
	path := filepath.Join(dir, name)
	trialJSON(t, path, map[string]any{
		"schema_version": 1, "set": set, "items": items,
	})
	return path
}

// A known-failure-set repair cannot hide a regression on the development
// population. The old pooled calculation was +25 points here (two D losses
// and four R rescues); D itself is -50 points.
func TestTrialQualityUsesDRepresentativeRatherThanPoolingR(t *testing.T) {
	gold := map[string]string{}
	for _, id := range []string{"d1", "d2", "d3", "d4", "r1", "r2", "r3", "r4"} {
		gold[id] = "c1"
	}
	f := newTrialFixture(t, gold)
	d := trialQualitySet(t, f.root, "set-d-quality.json", SetD, []string{"d1", "d2", "d3", "d4"})
	r := trialQualitySet(t, f.root, "set-r-quality.json", SetR, []string{"r1", "r2", "r3", "r4"})
	f.runner.Answer = func(item, config string) (string, string) {
		if strings.Contains(filepath.Base(config), "base") {
			if strings.HasPrefix(item, "r") {
				return OutcomeSelected, "c2"
			}
			return OutcomeSelected, "c1"
		}
		if item == "d1" || item == "d2" {
			return OutcomeSelected, "c2"
		}
		return OutcomeSelected, "c1"
	}
	card := f.card("d-is-representative", nil, func(m *map[string]any) {
		(*m)["stage"] = StageB
		(*m)["manifests"] = []string{d, r}
		(*m)["min_evaluable_items"] = 4
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if !q.Measured || q.Evaluable != 4 || q.QBase != 1 || q.QNew != 0.5 || q.DeltaPoints != -50 {
		t.Fatalf("the representative quality must be D only, got %+v", q)
	}
	if got := q.Sets[SetR]; got.Evaluable != 4 || got.DeltaPoints != 100 {
		t.Fatalf("R remains a separate repair diagnostic, got %+v", got)
	}
	if result.Report.Suggested.Value == DecisionFinalist {
		t.Fatalf("R repairs must not promote a D regression to finalist: %+v", result.Report.Suggested)
	}
}

// Plentiful R probes cannot make a three-item D representative look measured.
func TestTrialQualityFloorCountsOnlyD(t *testing.T) {
	gold := map[string]string{}
	for _, id := range []string{"d1", "d2", "d3", "r1", "r2", "r3", "r4"} {
		gold[id] = "c1"
	}
	f := newTrialFixture(t, gold)
	d := trialQualitySet(t, f.root, "set-d-floor.json", SetD, []string{"d1", "d2", "d3"})
	r := trialQualitySet(t, f.root, "set-r-floor.json", SetR, []string{"r1", "r2", "r3", "r4"})
	card := f.card("d-floor", nil, func(m *map[string]any) {
		(*m)["stage"] = StageB
		(*m)["manifests"] = []string{d, r}
		(*m)["min_evaluable_items"] = 4
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Measured || q.Evaluable != 3 || q.DeltaPoints != 0 {
		t.Fatalf("the D floor must reject this representative, got %+v", q)
	}
	if got := q.Sets[SetR]; got.Evaluable != 4 {
		t.Fatalf("R diagnostics are retained even though they cannot fill D's floor: %+v", got)
	}
	if result.Report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("an unmeasured D representative suggests %q, want %q", result.Report.Suggested.Value, DecisionInconclusive)
	}
}

// The stage-A early-cut exception remains global to D/R individual changes,
// even though the representative D result is too small to measure.
func TestTrialEarlyCutStillCountsDRChanges(t *testing.T) {
	f := newTrialFixture(t, map[string]string{
		"d1": "c1", "d2": "c1", "r1": "c1", "r2": "c1",
	})
	d := trialQualitySet(t, f.root, "set-d-cut.json", SetD, []string{"d1", "d2"})
	r := trialQualitySet(t, f.root, "set-r-cut.json", SetR, []string{"r1", "r2"})
	f.runner.Answer = func(item, config string) (string, string) {
		if strings.Contains(filepath.Base(config), "base") || strings.HasPrefix(item, "d") {
			return OutcomeSelected, "c1"
		}
		return OutcomeSelected, "c2"
	}
	card := f.card("global-early-cut", nil, func(m *map[string]any) {
		(*m)["manifests"] = []string{d, r}
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	if result.Report.Quality.NewlyWrong != 2 || result.Report.Quality.NewlyRight != 0 {
		t.Fatalf("the early-cut counts stay global to D/R, got %+v", result.Report.Quality)
	}
	if result.Report.Suggested.Value != DecisionDrop || result.Report.Suggested.Heuristic == "" {
		t.Fatalf("two R regressions still trigger the stage-A heuristic, got %+v", result.Report.Suggested)
	}
}

// An R-only probe reports its repair result by set, but it cannot become the
// representative quality result or a finalist recommendation.
func TestTrialQualityRProbeIsDiagnosticOnly(t *testing.T) {
	f := newTrialFixture(t, map[string]string{
		"r1": "c1", "r2": "c1", "r3": "c1", "r4": "c1",
	})
	r := trialQualitySet(t, f.root, "set-r-only.json", SetR, []string{"r1", "r2", "r3", "r4"})
	f.runner.Answer = func(_ string, config string) (string, string) {
		if strings.Contains(filepath.Base(config), "base") {
			return OutcomeSelected, "c2"
		}
		return OutcomeSelected, "c1"
	}
	card := f.card("r-only", nil, func(m *map[string]any) {
		(*m)["stage"] = StageB
		(*m)["manifests"] = []string{r}
		(*m)["min_evaluable_items"] = 4
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Metric != MetricSelectionTop1 || q.Measured || q.Evaluable != 0 {
		t.Fatalf("an R-only probe has no representative measurement, got %+v", q)
	}
	if got := q.Sets[SetR]; got.Evaluable != 4 || got.DeltaPoints != 100 {
		t.Fatalf("the R repair is still shown as a set diagnostic, got %+v", got)
	}
	if result.Report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("an R-only probe suggests %q, want %q", result.Report.Suggested.Value, DecisionInconclusive)
	}
}

// The A/B D rule must not erase the held-out H reading at stage C.
func TestTrialQualityStageCKeepsHRepresentative(t *testing.T) {
	f := newTrialFixture(t, map[string]string{
		"h1": "c1", "h2": "c1", "h3": "c1", "h4": "c1",
	})
	h := trialQualitySet(t, f.root, "set-h-quality.json", SetH, []string{"h1", "h2", "h3", "h4"})
	f.runner.Answer = func(_ string, config string) (string, string) {
		if strings.Contains(filepath.Base(config), "base") {
			return OutcomeSelected, "c2"
		}
		return OutcomeSelected, "c1"
	}
	card := f.card("h-is-representative", nil, func(m *map[string]any) {
		(*m)["stage"] = StageC
		(*m)["manifests"] = []string{h}
		(*m)["min_evaluable_items"] = 4
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if !q.Measured || q.Evaluable != 4 || q.DeltaPoints != 100 {
		t.Fatalf("stage C must retain H as its representative, got %+v", q)
	}
}

// When all weighted D strata are evaluable, their target shares define the
// representative result and the finalist rule. Here raw D regresses because
// math supplies four losses, while target-weighted D improves because writing
// has the larger target share and supplies two rescues.
func TestTrialWeightedDIsRepresentativeWhenAllStrataAreComplete(t *testing.T) {
	f := newTrialFixture(t, map[string]string{
		"m1": "c1", "m2": "c1", "m3": "c1", "m4": "c1",
		"w1": "c1", "w2": "c1",
	})
	d := filepath.Join(f.root, "weighted-complete-d.json")
	trialJSON(t, d, map[string]any{
		"schema_version": 1, "set": SetD,
		"items": []map[string]any{
			{"id": "m1", "strata": map[string]string{"category": "math"}},
			{"id": "m2", "strata": map[string]string{"category": "math"}},
			{"id": "m3", "strata": map[string]string{"category": "math"}},
			{"id": "m4", "strata": map[string]string{"category": "math"}},
			{"id": "w1", "strata": map[string]string{"category": "writing"}},
			{"id": "w2", "strata": map[string]string{"category": "writing"}},
		},
		"weights": map[string]float64{"math": 0.2, "writing": 0.8},
	})
	f.runner.Answer = func(item, config string) (string, string) {
		base := strings.Contains(filepath.Base(config), "base")
		if strings.HasPrefix(item, "m") {
			if base {
				return OutcomeSelected, "c1"
			}
			return OutcomeSelected, "c2"
		}
		if base {
			return OutcomeSelected, "c2"
		}
		return OutcomeSelected, "c1"
	}
	card := f.card("weighted-representative", nil, func(m *map[string]any) {
		(*m)["stage"] = StageB
		(*m)["manifests"] = []string{d}
		(*m)["min_evaluable_items"] = 6
		(*m)["min_items_per_category"] = 2
		(*m)["rules"] = map[string]any{"kind": RuleQuality}
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Weighted == nil || !q.Weighted.Measured {
		t.Fatalf("all weighted D strata are complete, got %+v", q.Weighted)
	}
	if raw := q.Sets[SetD].DeltaPoints; raw >= 0 {
		t.Fatalf("fixture needs a raw D regression, got %+v", q.Sets[SetD])
	}
	if !q.Measured || q.QBase != 0.2 || q.QNew != 0.8 || q.DeltaPoints != 60 {
		t.Fatalf("the representative result must use complete weighted D, got %+v", q)
	}
	if result.Report.Suggested.Value != DecisionFinalist {
		t.Fatalf("the finalist rule must read weighted D, got %+v", result.Report.Suggested)
	}
}

// A raw D total can clear its floor while a declared target stratum cannot.
// That is still unmeasured: dropping the thin stratum would silently change
// the target population.
func TestTrialWeightedDNeedsEveryTargetStratum(t *testing.T) {
	f := newTrialFixture(t, map[string]string{
		"m1": "c1", "m2": "c1", "w1": "c1",
	})
	d := filepath.Join(f.root, "weighted-incomplete-d.json")
	trialJSON(t, d, map[string]any{
		"schema_version": 1, "set": SetD,
		"items": []map[string]any{
			{"id": "m1", "strata": map[string]string{"category": "math"}},
			{"id": "m2", "strata": map[string]string{"category": "math"}},
			{"id": "w1", "strata": map[string]string{"category": "writing"}},
		},
		"weights": map[string]float64{"math": 0.5, "writing": 0.5},
	})
	f.runner.Answer = func(item, config string) (string, string) {
		if item != "w1" || strings.Contains(filepath.Base(config), "base") {
			return OutcomeSelected, "c1"
		}
		return OutcomeSelected, "c2"
	}
	card := f.card("weighted-incomplete", nil, func(m *map[string]any) {
		(*m)["stage"] = StageB
		(*m)["manifests"] = []string{d}
		(*m)["min_evaluable_items"] = 3
		(*m)["min_items_per_category"] = 2
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Evaluable != 3 || q.Measured {
		t.Fatalf("raw D reaches its floor but incomplete weighting must suppress it: %+v", q)
	}
	if q.Weighted == nil || q.Weighted.Measured || len(q.Weighted.Unevaluated) != 1 || q.Weighted.Unevaluated[0] != "writing" {
		t.Fatalf("the thin target stratum must remain an explicit diagnostic, got %+v", q.Weighted)
	}
	if result.Report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("incomplete target weighting suggests %q, want %q", result.Report.Suggested.Value, DecisionInconclusive)
	}
}
