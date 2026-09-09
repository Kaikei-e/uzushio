package judge

// Assessment is the fixed-H confirmation that follows a D/R trial.  It keeps
// the exploratory trial intact: Trial executes the two conditions, while this
// file owns the frozen inputs, holdout claim and fixed-sample recommendation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const AssessmentSchemaVersion = 1

const (
	AssessmentReportFile  = "assessment.json"
	AssessmentSummaryFile = "assessment.md"
	assessmentTrialDir    = "trial"
)

// Artifact is an immutable input named relative to the assessment card.
type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// FinalistArtifacts are the three files which establish the D/R finalist.
// The report says what was concluded, resume.json binds the binaries and
// inputs, and the journal binds the individual recorded measurements.
type FinalistArtifacts struct {
	Report  Artifact `json:"report"`
	Resume  Artifact `json:"resume"`
	Results Artifact `json:"results"`
}

type AssessmentRules struct {
	Alpha                         float64 `json:"alpha"`
	NoninferiorityMargin          float64 `json:"noninferiority_margin"`
	QualityFloor                  float64 `json:"quality_floor"`
	MinClusters                   int     `json:"min_clusters"`
	MaxObservedSeriousRegressions int     `json:"max_observed_serious_regressions"`
}

// AssessmentCard is deliberately separate from TrialCard.  It embeds the
// stage-C TrialCard only as an execution plan; its own fields are the frozen
// adoption inputs and rule.
type AssessmentCard struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Finalist      FinalistArtifacts `json:"finalist"`
	Trial         TrialCard         `json:"trial"`
	Dataset       Artifact          `json:"dataset"`
	HoldoutSHA256 string            `json:"holdout_sha256"`
	Labels        Artifact          `json:"labels"`
	Rules         AssessmentRules   `json:"rules"`
	Dir           string            `json:"-"`
	Digest        string            `json:"-"`
}

type AssessmentOptions struct {
	CardPath string
	CMoA     string
	Out      string
	Vault    string
	// Registry is a shared, durable directory.  It is required so H cannot be
	// silently opened again by a later candidate.
	Registry string
	DryRun   bool
	Resume   bool
	Runner   TrialRunner
	Now      func() time.Time
	Log      func(string)
}

type holdoutUse struct {
	SchemaVersion      int    `json:"schema_version"`
	HoldoutSHA256      string `json:"holdout_sha256"`
	AssessmentID       string `json:"assessment_id"`
	AssessmentSHA256   string `json:"assessment_sha256"`
	RuntimeFingerprint string `json:"runtime_fingerprint"`
	CandidateID        string `json:"candidate_id"`
	OpenedAt           string `json:"opened_at"`
	Source             string `json:"source"`
	SourceID           string `json:"source_id"`
	ConversationSHA256 string `json:"conversation_sha256"`
	Cluster            string `json:"cluster"`
	KeyKind            string `json:"key_kind"`
}

func LoadAssessmentCard(name string) (AssessmentCard, error) {
	body, err := os.ReadFile(name) //nolint:gosec // caller names a card
	if err != nil {
		return AssessmentCard{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var card AssessmentCard
	if err := decodeStrict(strings.NewReader(string(body)), &card); err != nil {
		return AssessmentCard{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	card.Dir = filepath.Dir(name)
	sum := sha256.Sum256(body)
	card.Digest = hex.EncodeToString(sum[:])
	card.Trial.Dir = card.Dir
	if err := card.validate(name); err != nil {
		return AssessmentCard{}, err
	}
	return card, nil
}

func (c AssessmentCard) path(name string) string {
	if filepath.IsAbs(name) || name == "" {
		return name
	}
	return filepath.Join(c.Dir, filepath.FromSlash(name))
}

func (c *AssessmentCard) validate(name string) error {
	if c.SchemaVersion != AssessmentSchemaVersion {
		return fmt.Errorf("%w: %s is schema version %d, this build reads %d", ErrJudge, name, c.SchemaVersion, AssessmentSchemaVersion)
	}
	if c.ID == "" {
		return fmt.Errorf("%w: %s names no assessment", ErrJudge, name)
	}
	for what, artifact := range map[string]Artifact{
		"finalist.report": c.Finalist.Report, "finalist.resume": c.Finalist.Resume,
		"finalist.results": c.Finalist.Results, "dataset": c.Dataset, "labels": c.Labels,
	} {
		if artifact.Path == "" || !sha256String(artifact.SHA256) {
			return fmt.Errorf("%w: %s names no valid %s path and sha256", ErrJudge, name, what)
		}
	}
	if !sha256String(c.HoldoutSHA256) {
		return fmt.Errorf("%w: %s names no valid holdout_sha256", ErrJudge, name)
	}
	// Absence has a meaning in ordinary trials (it defaults to none); an
	// assessment makes freshness part of its frozen contract, so spell it.
	if c.Trial.Reuse.Kind != ReuseNone {
		return fmt.Errorf("%w: %s assessment trial must explicitly set reuse kind %q", ErrJudge, name, ReuseNone)
	}
	if err := c.Trial.validate(name + ".trial"); err != nil {
		return err
	}
	if c.Trial.Stage != StageC || c.Trial.Reuse.Kind != ReuseNone {
		return fmt.Errorf("%w: %s assessment trial must be stage C with reuse kind %q", ErrJudge, name, ReuseNone)
	}
	if !c.Trial.AlternatingBlocks || c.Trial.BlockSize <= 0 {
		return fmt.Errorf("%w: %s assessment trial must counterbalance with alternating_blocks and block_size", ErrJudge, name)
	}
	if len(c.Trial.Manifests) != 1 {
		return fmt.Errorf("%w: %s assessment trial names %d manifests; fixed H assessment names exactly one", ErrJudge, name, len(c.Trial.Manifests))
	}
	if len(c.Trial.Take) != 1 || c.Trial.Take[SetH] <= 0 {
		return fmt.Errorf("%w: %s assessment trial must declare a positive full H take", ErrJudge, name)
	}
	if c.Trial.Base.ID == "" || c.Trial.Candidate.ID == "" || c.Trial.Base.ID == c.Trial.Candidate.ID {
		return fmt.Errorf("%w: %s assessment trial needs two distinct condition ids", ErrJudge, name)
	}
	for _, condition := range []TrialCondition{c.Trial.Base, c.Trial.Candidate} {
		if !sha256String(condition.ConfigSHA256) {
			return fmt.Errorf("%w: %s condition %s has no pinned config_sha256", ErrJudge, name, condition.ID)
		}
		if condition.Switch != nil {
			return fmt.Errorf("%w: %s assessment conditions cannot use switches; fixed H confirms one pinned fleet", ErrJudge, name)
		}
	}
	r := c.Rules
	if r.Alpha <= 0 || r.Alpha >= 1 || r.NoninferiorityMargin < 0 || r.NoninferiorityMargin > 1 ||
		r.QualityFloor < 0 || r.QualityFloor > 1 || r.MinClusters < 2 || r.MaxObservedSeriousRegressions < 0 {
		return fmt.Errorf("%w: %s has invalid fixed assessment rules", ErrJudge, name)
	}
	return nil
}

func sha256String(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func artifactDigest(card AssessmentCard, artifact Artifact) error {
	digest, err := ConfigDigest(card.path(artifact.Path))
	if err != nil {
		return err
	}
	if digest != artifact.SHA256 {
		return fmt.Errorf("%w: %s hashes to %s; card pinned %s", ErrJudge, artifact.Path, assessShort(digest), assessShort(artifact.SHA256))
	}
	return nil
}

// Assess runs one previously selected finalist once on frozen H.  A dry run
// validates only the card syntax: it intentionally neither hashes nor opens H
// data/labels and cannot spend the holdout.
func Assess(ctx context.Context, opts AssessmentOptions) (*AssessmentReport, error) {
	if opts.CardPath == "" || opts.Registry == "" {
		return nil, fmt.Errorf("%w: assessment requires --card and a shared holdout registry", ErrJudge)
	}
	card, err := LoadAssessmentCard(opts.CardPath)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		return &AssessmentReport{SchemaVersion: AssessmentSchemaVersion, CardID: card.ID, CardSHA256: card.Digest,
			Finalist: card.Finalist.Report.SHA256, Holdout: card.HoldoutSHA256, TrialID: card.Trial.ID,
			BaseID: card.Trial.Base.ID, CandidateID: card.Trial.Candidate.ID, Recommendation: AssessmentInconclusive,
			Notes: []string{"dry run: H dataset and labels were not read, hashed, claimed, or measured"}}, nil
	}
	if opts.Out == "" {
		opts.Out = filepath.Join(card.Dir, "assessment-"+card.ID)
	}
	if opts.Vault == "" {
		opts.Vault = "."
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Runner == nil {
		opts.Runner = CMoATrialRunner{Binary: opts.CMoA, Log: opts.Log}
	}

	// The candidate/config/binary checks precede the holdout claim.  They do
	// not read H and prevent a malformed request from consuming it.
	final, resume, finalistRecords, err := finalist(card)
	if err != nil {
		return nil, err
	}
	if err := checkFinalist(card, final, resume, finalistRecords, opts); err != nil {
		return nil, err
	}
	runtime, err := assessmentRuntimeFingerprint(card, opts)
	if err != nil {
		return nil, err
	}
	// The metadata packet has no task body or labels, so it can be read before
	// the claim.  Claim every stable H source/conversation separately: a later
	// subset or superset then intersects an existing entry instead of acquiring
	// a new whole-set digest and calling overlapping items unused.
	if err := artifactDigest(card, card.Dataset); err != nil {
		return nil, err
	}
	dataset, err := LoadEvaluationDataset(card.path(card.Dataset.Path))
	if err != nil {
		return nil, err
	}
	if dataset.HoldoutDigest() != card.HoldoutSHA256 {
		return nil, fmt.Errorf("%w: evaluation dataset holdout digest does not match the frozen card", ErrJudge)
	}
	holdout := dataset.ItemsFor(SetH)
	if len(holdout) == 0 || len(holdout) != card.Trial.Take[SetH] || card.Trial.MaxItems != len(holdout) {
		return nil, fmt.Errorf("%w: assessment H take/max_items must equal the frozen dataset's %d H item(s)", ErrJudge, len(holdout))
	}
	if err := checkHoldoutNotInFinalist(holdout, resume); err != nil {
		return nil, err
	}
	if err := claimHoldout(card, opts.Registry, runtime, holdout, opts.Resume, opts.Now()); err != nil {
		return nil, err
	}

	// Everything below opens H task content or labels. Claims deliberately
	// remain if an input pin fails or the fleet fails, so no other experiment
	// can call an overlapping item unused.
	if err := artifactDigest(card, card.Labels); err != nil {
		return nil, err
	}
	labels, err := LoadEvaluationLabels(card.path(card.Labels.Path), holdout, 2)
	if err != nil {
		return nil, err
	}

	suite, err := LoadSuite(card.Trial.Path(card.Trial.Suite))
	if err != nil {
		return nil, err
	}
	manifest, err := LoadTrialManifest(card.Trial.Path(card.Trial.Manifests[0]))
	if err != nil {
		return nil, err
	}
	if manifest.Set != SetH || !sameIDs(manifest.Items, holdout) {
		return nil, fmt.Errorf("%w: assessment H manifest and frozen dataset do not name the same items", ErrJudge)
	}
	if err := checkAssessmentInputs(suite, holdout); err != nil {
		return nil, err
	}
	if err := VerifyEvaluationLabelCandidates(labels, suite, holdout); err != nil {
		return nil, err
	}
	if err := CheckPlan(card.Trial, []TrialManifest{manifest}); err != nil {
		return nil, err
	}
	trialOut := filepath.Join(opts.Out, assessmentTrialDir)
	result, err := Trial(ctx, TrialOptions{Card: card.Trial, Suite: suite, Manifests: []TrialManifest{manifest},
		Runner: opts.Runner, Out: trialOut, Resume: opts.Resume, Vault: opts.Vault, Now: opts.Now, Log: opts.Log})
	if err != nil {
		return nil, err
	}
	if err := result.Write(trialOut); err != nil {
		return nil, err
	}
	report, err := assessReport(card, dataset, labels, result)
	if err != nil {
		return nil, err
	}
	if err := report.Write(opts.Out); err != nil {
		return nil, err
	}
	return &report, nil
}

func finalist(card AssessmentCard) (TrialReport, trialResumeState, []TrialRecord, error) {
	for _, artifact := range []Artifact{card.Finalist.Report, card.Finalist.Resume, card.Finalist.Results} {
		if err := artifactDigest(card, artifact); err != nil {
			return TrialReport{}, trialResumeState{}, nil, err
		}
	}
	var report TrialReport
	if err := readJSONFile(card.path(card.Finalist.Report.Path), &report); err != nil {
		return TrialReport{}, trialResumeState{}, nil, err
	}
	var resume trialResumeState
	if err := readJSONFile(card.path(card.Finalist.Resume.Path), &resume); err != nil {
		return TrialReport{}, trialResumeState{}, nil, err
	}
	records, err := ReadTrialRecords(card.path(card.Finalist.Results.Path))
	if err != nil {
		return TrialReport{}, trialResumeState{}, nil, err
	}
	return report, resume, records, nil
}

func checkFinalist(card AssessmentCard, report TrialReport, resume trialResumeState, records []TrialRecord, opts AssessmentOptions) error {
	if report.Card.Stage != StageB || report.Suggested.Value != DecisionFinalist || report.Recorded != RecordedResult || report.StopReason != StopCompleted || report.Interrupted != 0 {
		return fmt.Errorf("%w: finalist report is not a completed stage-B finalist", ErrJudge)
	}
	if report.Plan.Sets[SetD] == 0 || report.Plan.Sets[SetR] == 0 {
		return fmt.Errorf("%w: finalist report does not contain both D and R", ErrJudge)
	}
	for set := range report.Plan.Sets {
		if set != SetD && set != SetR {
			return fmt.Errorf("%w: finalist report contains set %s; a finalist is developed on D/R only", ErrJudge, set)
		}
	}
	if report.Card.Base.ID != card.Trial.Base.ID || report.Card.Candidate.ID != card.Trial.Candidate.ID ||
		report.Card.Seed != card.Trial.Seed || report.Card.JudgeSeed != card.Trial.JudgeSeed ||
		report.Card.Base.ConfigSHA256 != card.Trial.Base.ConfigSHA256 ||
		report.Card.Candidate.ConfigSHA256 != card.Trial.Candidate.ConfigSHA256 {
		return fmt.Errorf("%w: assessment conditions, config pins, or seeds differ from the D/R finalist", ErrJudge)
	}
	for _, condition := range []TrialCondition{card.Trial.Base, card.Trial.Candidate} {
		digest, err := ConfigDigest(card.Trial.Path(condition.Config))
		if err != nil {
			return err
		}
		if digest != condition.ConfigSHA256 {
			return fmt.Errorf("%w: assessment condition %s configuration hashes to %s; card pinned %s", ErrJudge, condition.ID, assessShort(digest), assessShort(condition.ConfigSHA256))
		}
	}
	if resume.SchemaVersion != TrialSchemaVersion {
		return fmt.Errorf("%w: finalist resume identity has no condition binary digests", ErrJudge)
	}
	if _, ok := resume.Binaries[ConditionBase]; !ok {
		return fmt.Errorf("%w: finalist resume identity has no base binary digest", ErrJudge)
	}
	if _, ok := resume.Binaries[ConditionCandidate]; !ok {
		return fmt.Errorf("%w: finalist resume identity has no candidate binary digest", ErrJudge)
	}
	if err := bindFinalistArtifacts(report, resume, records); err != nil {
		return err
	}
	for condition, want := range map[string]string{ConditionBase: resume.Binaries[ConditionBase], ConditionCandidate: resume.Binaries[ConditionCandidate]} {
		got, err := trialBinary(TrialOptions{Card: card.Trial, Runner: opts.Runner}, condition)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%w: assessment %s binary hashes to %s; finalist used %s", ErrJudge, condition, assessShort(got), assessShort(want))
		}
	}
	return nil
}

// bindFinalistArtifacts checks the relationship between the three pinned
// finalist files. Hashing files independently proves only that each exists;
// this checks that they are the report, resume identity and journal of one
// fresh D/R trial.
func bindFinalistArtifacts(report TrialReport, resume trialResumeState, records []TrialRecord) error {
	if resume.Card.ID == "" || len(resume.Plan) == 0 {
		return fmt.Errorf("%w: finalist resume identity predates its canonical card and plan", ErrJudge)
	}
	if !sameFinalistCard(report.Card, resume.Card) {
		return fmt.Errorf("%w: finalist report card differs from its resume identity", ErrJudge)
	}
	planned := plannedItems(resume.Plan)
	if report.Planned != len(planned) || report.Completed != len(planned) || report.Interrupted != 0 ||
		report.Reuse.Kind != ReuseNone || report.Reuse.Reused != 0 || report.Reuse.Measured != len(resume.Plan) {
		return fmt.Errorf("%w: finalist report does not describe a complete fresh plan", ErrJudge)
	}
	byStep := map[TrialStep]TrialRecord{}
	for _, record := range records {
		if _, duplicate := byStep[record.Key()]; duplicate {
			return fmt.Errorf("%w: finalist journal repeats step %s/%s", ErrJudge, record.Item, record.Condition)
		}
		byStep[record.Key()] = record
	}
	if len(byStep) != len(resume.Plan) {
		return fmt.Errorf("%w: finalist journal holds %d steps, resume plan holds %d", ErrJudge, len(byStep), len(resume.Plan))
	}
	for _, step := range resume.Plan {
		if step.Set != SetD && step.Set != SetR || (step.Condition != ConditionBase && step.Condition != ConditionCandidate) {
			return fmt.Errorf("%w: finalist resume plan is not D/R paired work", ErrJudge)
		}
		record, ok := byStep[step]
		wantID := report.Card.Base.ID
		if step.Condition == ConditionCandidate {
			wantID = report.Card.Candidate.ID
		}
		if !ok || record.Source != SourceMeasured || record.ConditionID != wantID || !record.Measured || record.Error != "" || record.ReuseKey == "" || record.RunDir == "" {
			return fmt.Errorf("%w: finalist journal has no complete fresh measurement for %s/%s", ErrJudge, step.Item, step.Condition)
		}
	}
	if len(report.Items) != len(planned) {
		return fmt.Errorf("%w: finalist report items do not match its plan", ErrJudge)
	}
	reported := map[TrialStep]bool{}
	for _, item := range report.Items {
		itemKey := TrialStep{Item: item.Item, Set: item.Set}
		if reported[itemKey] {
			return fmt.Errorf("%w: finalist report repeats item %s", ErrJudge, item.Item)
		}
		reported[itemKey] = true
		base, baseOK := byStep[TrialStep{Item: item.Item, Set: item.Set, Condition: ConditionBase}]
		candidate, candidateOK := byStep[TrialStep{Item: item.Item, Set: item.Set, Condition: ConditionCandidate}]
		if !baseOK || !candidateOK || !item.Complete || item.BaseOutcome != base.Outcome || item.NewOutcome != candidate.Outcome || item.BaseChoice != base.Candidate || item.NewChoice != candidate.Candidate {
			return fmt.Errorf("%w: finalist report item %s differs from its journal", ErrJudge, item.Item)
		}
	}
	return nil
}

func sameFinalistCard(report, resume TrialCard) bool {
	// Reports redact only machine-local paths.  Clear them on both sides, and
	// compare every semantic card field including conditions, seeds and rules.
	for _, card := range []*TrialCard{&report, &resume} {
		card.BudgetSeconds = 0
		card.Dir, card.Suite, card.Reuse.Source = "", "", ""
		card.Manifests, card.Labels = nil, nil
		card.Base.Config, card.Candidate.Config = "", ""
	}
	return reflect.DeepEqual(report, resume)
}

func assessmentRuntimeFingerprint(card AssessmentCard, opts AssessmentOptions) (string, error) {
	base, err := trialBinary(TrialOptions{Card: card.Trial, Runner: opts.Runner}, ConditionBase)
	if err != nil {
		return "", err
	}
	candidate, err := trialBinary(TrialOptions{Card: card.Trial, Runner: opts.Runner}, ConditionCandidate)
	if err != nil {
		return "", err
	}
	out, err := filepath.Abs(opts.Out)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize assessment output: %w", ErrJudge, err)
	}
	body, err := json.Marshal(struct {
		Card, Base, Candidate, Out string
	}{card.Digest, base, candidate, filepath.Clean(out)})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func checkHoldoutNotInFinalist(items []EvaluationItem, resume trialResumeState) error {
	for _, item := range items {
		for _, digest := range resume.Files {
			if digest == item.ConversationSHA256 {
				return fmt.Errorf("%w: frozen H item %s has the same conversation digest as a D/R finalist input", ErrJudge, item.ID)
			}
		}
	}
	return nil
}

func claimHoldout(card AssessmentCard, registry, runtime string, items []EvaluationItem, resume bool, now time.Time) error {
	if err := os.MkdirAll(registry, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	claimedHere := map[string]bool{}
	for _, item := range items {
		for _, key := range holdoutKeys(item) {
			if err := claimHoldoutItem(card, registry, runtime, item, key, resume, claimedHere[key.name], now); err != nil {
				return err
			}
			claimedHere[key.name] = true
		}
	}
	return nil
}

type holdoutKey struct{ kind, name string }

func claimHoldoutItem(card AssessmentCard, registry, runtime string, item EvaluationItem, key holdoutKey, resume, claimedHere bool, now time.Time) error {
	name := filepath.Join(registry, key.kind+"-"+key.name+".json")
	claim := holdoutUse{SchemaVersion: AssessmentSchemaVersion, HoldoutSHA256: card.HoldoutSHA256,
		AssessmentID: card.ID, AssessmentSHA256: card.Digest, RuntimeFingerprint: runtime,
		CandidateID: card.Trial.Candidate.ID, OpenedAt: now.UTC().Format(time.RFC3339),
		Source: item.Source, SourceID: item.SourceID, ConversationSHA256: item.ConversationSHA256,
		Cluster: item.Cluster, KeyKind: key.kind}
	body, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		defer func() { _ = file.Close() }()
		if _, err := file.Write(append(body, '\n')); err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	oldBody, err := os.ReadFile(name) //nolint:gosec // registry key is stable provenance
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var old holdoutUse
	if err := json.Unmarshal(oldBody, &old); err != nil || old.KeyKind != key.kind || old.AssessmentSHA256 != claim.AssessmentSHA256 || old.RuntimeFingerprint != claim.RuntimeFingerprint || (!resume && !claimedHere) {
		return fmt.Errorf("%w: holdout item %s was claimed by a different assessment or runtime", ErrJudge, assessShort(key.name))
	}
	// One source/task cluster can contain several turns.  A same-assessment
	// resume may see another turn's group key; its source and conversation
	// keys still independently prevent provenance substitution.
	if key.kind == "source_cluster" && (resume || claimedHere) {
		return nil
	}
	if old.Source != item.Source || old.SourceID != item.SourceID || old.ConversationSHA256 != item.ConversationSHA256 || old.Cluster != item.Cluster {
		return fmt.Errorf("%w: holdout item %s provenance differs from its existing claim", ErrJudge, assessShort(key.name))
	}
	return nil
}

func holdoutKeys(item EvaluationItem) []holdoutKey {
	return []holdoutKey{
		{kind: "conversation", name: holdoutDigest("conversation", item.ConversationSHA256)},
		{kind: "source", name: holdoutDigest("source", item.Source, item.SourceID)},
		{kind: "source_cluster", name: holdoutDigest("source_cluster", item.Source, item.Cluster)},
	}
}

func holdoutDigest(parts ...string) string {
	body, _ := json.Marshal(parts)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func sameIDs(manifest []TrialManifestItem, items []EvaluationItem) bool {
	if len(manifest) != len(items) {
		return false
	}
	got, want := make([]string, 0, len(manifest)), make([]string, 0, len(items))
	for _, item := range manifest {
		got = append(got, item.ID)
	}
	for _, item := range items {
		want = append(want, item.ID)
	}
	return slices.Equal(got, want)
}

// checkAssessmentInputs binds the metadata packet to the exact task inputs
// Trial is about to hand to CMoA.  It is intentionally after claimHoldout:
// opening the conversation is opening H.
func checkAssessmentInputs(suite Suite, items []EvaluationItem) error {
	tasks := map[string]Task{}
	for _, task := range suite.Tasks {
		tasks[task.ID] = task
	}
	for _, item := range items {
		task, ok := tasks[item.ID]
		if !ok {
			return fmt.Errorf("%w: frozen H item %s is absent from suite %s", ErrJudge, item.ID, suite.ID)
		}
		var spec struct {
			Conversation string `json:"conversation"`
		}
		if err := readJSONFile(filepath.Join(suite.TaskDir(task), "task.json"), &spec); err != nil {
			return err
		}
		if spec.Conversation == "" {
			return fmt.Errorf("%w: frozen H item %s has no task conversation", ErrJudge, item.ID)
		}
		body, err := os.ReadFile(filepath.Join(suite.TaskDir(task), filepath.FromSlash(spec.Conversation))) //nolint:gosec // suite task input
		if err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != item.ConversationSHA256 {
			return fmt.Errorf("%w: frozen H item %s conversation digest differs from the task input", ErrJudge, item.ID)
		}
		if !slices.Equal(item.Candidates, Positions) {
			return fmt.Errorf("%w: frozen H item %s candidate ids differ from the trial positions", ErrJudge, item.ID)
		}
	}
	return nil
}

func assessShort(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
