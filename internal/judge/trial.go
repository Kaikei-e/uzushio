package judge

// A trial is the cheap half of judging a change to the judge.
//
// A calibration measures one judge against people over a whole suite. That is
// the expensive reading, and making it the gate on every experiment is what
// turns "try the smaller reasoning budget" into an afternoon. A trial is the
// other reading: two conditions, a handful of items chosen in advance, a time
// box, and a comparison that says whether the change is worth a calibration.
//
// Three things keep it honest, and all three are refusals rather than features.
//
//   - It reuses a saved run only when the run answers the same question. The
//     reuse key names every input a judge's answer depends on — the item, the
//     candidate texts and which identifier each of them carries, the prompt
//     version, the harness build, the judge's own settings, both seeds, the
//     selection rule — and a run whose key differs is not the base condition,
//     it is a different measurement that happens to be about the same item.
//   - It never carries an old wall time into a speed comparison. A reused base
//     run is free, and free is exactly why its seconds are worthless: they were
//     spent on another day, on another load, possibly on another fleet. Where
//     the base was reused the trial reports no speed ratio at all rather than a
//     ratio nobody should read.
//   - It suggests a decision and does not make one. The `decision` field of an
//     experiment card is a person's; what a trial writes is `suggested`, with
//     the rule that produced it named, and with the two-item heuristic labelled
//     as a heuristic. A trial of four items is a development read, and no
//     wording in this file calls it a confirmed one.
//
// The design is the owner's internal research memo of 2026-09-06, section 4,
// and ADR 0010 records it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TrialSchemaVersion is the version of the card, the manifest and the report
// this build reads and writes.
const TrialSchemaVersion = 1

// The three roles an item set can play. They are kept apart because an item
// used to tune a change cannot also be the item that confirms it.
const (
	// SetD is the development representative set: stratified, at most forty
	// items, the one a change is compared on.
	SetD = "D"
	// SetR is the known-failure set: the four to eight items that already go
	// wrong, kept separate so their score is never averaged into D's.
	SetR = "R"
	// SetH is the set held back from development entirely, for the adoption
	// check. A trial may read it; a trial that tuned on it has spent it.
	SetH = "H"
)

// The three stages, and the time box each one is planned against. The boxes
// are the memo's operating design values rather than measured durations: what
// they bound is how long an experiment may run before it has to say something.
const (
	StageA = "A"
	StageB = "B"
	StageC = "C"
)

// The default budget of each stage, in seconds. B is cumulative and includes
// A; C is an initial estimate for a separate window.
const (
	BudgetA = 600
	BudgetB = 1800
	BudgetC = 5400
)

// The purposes a change can have, which decide which pass rule applies.
const (
	// RuleSpeed buys time and may pay for it in quality.
	RuleSpeed = "speed"
	// RuleQuality buys quality and may pay for it in time.
	RuleQuality = "quality"
	// RuleOutputFix repairs a named failure and must not cost quality.
	RuleOutputFix = "output_fix"
)

// The stopping vocabulary. The values are stable English so a report can be
// read by a program; TrialStopLabel carries the memo's own Japanese word so it
// can be read by the person who wrote the rule.
const (
	// StopCompleted is every planned step done inside the box. It is this
	// package's own sixth value: the memo names five ways to stop early and
	// does not name the way a run ends when nothing goes wrong.
	StopCompleted = "completed"
	// StopMalfunction is an unsupported flag, a broken schema, a candidate
	// mapping that does not line up, or a reproduced serious violation.
	StopMalfunction = "malfunction"
	// StopEarlyCut is the development heuristic of stage A.
	StopEarlyCut = "early_cut"
	// StopNoProspect is stage B finding neither quality nor time.
	StopNoProspect = "no_prospect"
	// StopOutOfBudget is the time box ending the run. It is not the model
	// failing, and it never reads as a verdict about the change.
	StopOutOfBudget = "out_of_budget"
	// StopUndetermined is a run that finished and settled nothing.
	StopUndetermined = "undetermined"
)

// TrialStopLabel is the memo's word for a stop reason.
func TrialStopLabel(reason string) string {
	switch reason {
	case StopCompleted:
		return "完走"
	case StopMalfunction:
		return "動作不良"
	case StopEarlyCut:
		return "早い見切り"
	case StopNoProspect:
		return "見込みなし"
	case StopOutOfBudget:
		return "時間・資源切れ"
	case StopUndetermined:
		return "未確定"
	}
	return ""
}

// The decisions a trial may suggest. `adopt` is deliberately absent: adoption
// is the one verdict that is a claim about a population, a trial is not
// powered to make it, and a runner that could print the word would eventually
// print it.
const (
	DecisionDrop         = "drop"
	DecisionAdvance      = "advance"
	DecisionFinalist     = "finalist"
	DecisionInconclusive = "inconclusive"
)

// The two conditions of a trial, as they are named in the plan, the results
// journal and the report.
const (
	ConditionBase      = "base"
	ConditionCandidate = "candidate"
)

// Where a condition's answer for one item came from.
const (
	// SourceMeasured is a judge that was asked, in this trial, just now.
	SourceMeasured = "measured"
	// SourceReused is a run read back off disk because its reuse key matched.
	SourceReused = "reused"
)

// The kinds of reuse source a card can name.
const (
	// ReuseNone measures both conditions.
	ReuseNone = "none"
	// ReuseSavedRuns scans `<source>/<item dir>/runs/*`, which is where the
	// harness leaves a run made beside its item.
	ReuseSavedRuns = "saved_runs"
	// ReuseCalibration reads a calibration report directory's items.jsonl and
	// takes the run directories it names.
	ReuseCalibration = "calibration"
	// ReuseTrial reads an earlier trial's results journal.
	ReuseTrial = "trial"
)

// The file names a trial writes into its output directory.
const (
	// TrialResultsFile is the resume unit's record: one line per item and
	// condition, appended as each completes. `Judge.Run` does not promise
	// resumption inside a run, so the item is the smallest thing a trial
	// re-does, and a line here is the promise never to re-do it.
	TrialResultsFile = "results.jsonl"
	// TrialReportFile is the comparison, as JSON.
	TrialReportFile = "trial.json"
	// TrialSummaryFile is the same comparison as a short Markdown note.
	TrialSummaryFile = "trial.md"
)

// TrialCard is the pre-registration: everything decided before the first call.
//
// It is written before a run and echoed unchanged into the report, which is
// the whole of its job. A threshold moved after the numbers are in is not a
// threshold, and the only way to tell the two apart afterwards is to have the
// card in the same file as the result.
type TrialCard struct {
	SchemaVersion int `json:"schema_version"`
	// ID names this experiment, Hypothesis says what is expected and Change
	// says what single thing differs between the conditions.
	ID         string `json:"id"`
	Hypothesis string `json:"hypothesis"`
	Change     string `json:"change"`
	// Stage is A, B or C.
	Stage string `json:"stage"`
	// Suite is the chat suite manifest the items are read out of.
	Suite string `json:"suite"`
	// Manifests are the D/R/H item manifests, in the order their items are
	// run. The order of the plan is the order of these files and of the items
	// inside them, and it does not change between conditions or between runs.
	Manifests []string `json:"manifests"`
	// Labels are human label files, as `judge calibrate` reads them.
	Labels []string `json:"labels,omitempty"`
	// Base and Candidate are the two conditions.
	Base      TrialCondition `json:"base"`
	Candidate TrialCondition `json:"candidate"`
	// Reuse says where a base answer may be read back from instead of asked.
	Reuse TrialReuse `json:"reuse"`
	// Seed is the presentation seed and JudgeSeed the judge's sampling seed.
	// One seed is the default: what a single seed drops is repetition, and it
	// never drops either order of a pair.
	Seed      int `json:"seed"`
	JudgeSeed int `json:"judge_seed"`
	// Metrics names what this experiment reads, for the record.
	Metrics []string `json:"metrics,omitempty"`
	// Rules are the pass and drop thresholds, fixed in advance.
	Rules TrialRules `json:"rules"`
	// BudgetSeconds is the wall clock the run may spend launching work. Zero
	// takes the stage's default.
	BudgetSeconds int `json:"budget_seconds"`
	// AlternatingBlocks measures the two conditions in alternating blocks
	// rather than item by item. It is for a change to the runtime — cache,
	// parallelism, the server — where a whole-condition-then-whole-condition
	// order would confound the change with whatever the machine was doing.
	AlternatingBlocks bool `json:"alternating_blocks,omitempty"`
	BlockSize         int  `json:"block_size,omitempty"`
	// MinItemsPerCategory is how many evaluable items a stratum needs before
	// the report gives it a number rather than calling it unevaluated. Zero
	// takes DefaultMinItemsPerCategory.
	MinItemsPerCategory int `json:"min_items_per_category,omitempty"`
	// Dir is the directory the card was read from; every relative path in it
	// is resolved against this.
	Dir string `json:"-"`
}

// TrialCondition is one side of the comparison.
type TrialCondition struct {
	// ID names the condition. A fix to a condition takes a new identifier
	// rather than the same one twice.
	ID string `json:"id"`
	// Config is the harness configuration this condition runs under.
	Config string `json:"config"`
	// ConfigSHA256 is what that file hashed to when the card was written. It
	// is checked before anything is spent: a local configuration is edited
	// between experiments, and a trial that silently ran the edited one would
	// be comparing two things nobody wrote down.
	ConfigSHA256 string `json:"config_sha256,omitempty"`
}

// TrialReuse names where base answers may come from.
type TrialReuse struct {
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
}

// TrialRules are the thresholds, in the memo's units: quality in points on a
// 0–100 scale over the evaluable items, time as a ratio of means.
type TrialRules struct {
	// Kind is speed, quality or output_fix.
	Kind string `json:"kind"`
	// MinDeltaQPoints is the quality difference the candidate has to reach.
	// Nil takes the default for the kind.
	MinDeltaQPoints *float64 `json:"min_delta_q_points,omitempty"`
	// MaxTimeRatio is the largest mean-time ratio new/base that still passes.
	// Nil takes the default for the kind.
	MaxTimeRatio *float64 `json:"max_time_ratio,omitempty"`
	// Targets are the items an output_fix change is supposed to repair.
	Targets []string `json:"targets,omitempty"`
}

// Thresholds answers the two numbers, filling in the memo's defaults.
//
// They are design values for starting a comparison and not lines any published
// work draws. On forty items one item is 2.5 points, so a −2 point
// non-inferiority is not something a stage B run can demonstrate; what the
// number does is decide which changes are worth measuring properly.
func (r TrialRules) Thresholds() (minDeltaQ, maxTimeRatio float64) {
	switch r.Kind {
	case RuleSpeed:
		minDeltaQ, maxTimeRatio = -2, 0.90
	case RuleQuality:
		minDeltaQ, maxTimeRatio = 3, 1.05
	case RuleOutputFix:
		minDeltaQ, maxTimeRatio = 0, 0
	}
	if r.MinDeltaQPoints != nil {
		minDeltaQ = *r.MinDeltaQPoints
	}
	if r.MaxTimeRatio != nil {
		maxTimeRatio = *r.MaxTimeRatio
	}
	return minDeltaQ, maxTimeRatio
}

// DefaultMinItemsPerCategory is the floor under a per-stratum number.
//
// It is four because four is the smallest set stage A is allowed to run at
// all: a stratum thinner than the whole first stage has not been evaluated,
// and printing a mean over two items invites somebody to read it as one.
const DefaultMinItemsPerCategory = 4

// LoadTrialCard reads and validates an experiment card.
func LoadTrialCard(name string) (TrialCard, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the card
	if err != nil {
		return TrialCard{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var card TrialCard
	if err := json.Unmarshal(body, &card); err != nil {
		return TrialCard{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	card.Dir = filepath.Dir(name)
	if err := card.validate(name); err != nil {
		return TrialCard{}, err
	}
	return card, nil
}

func (c *TrialCard) validate(name string) error {
	if c.SchemaVersion != TrialSchemaVersion {
		return fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrJudge, name, c.SchemaVersion, TrialSchemaVersion)
	}
	if c.ID == "" {
		return fmt.Errorf("%w: %s names no experiment", ErrJudge, name)
	}
	if c.Hypothesis == "" || c.Change == "" {
		return fmt.Errorf("%w: %s states no hypothesis or no change; a card written "+
			"after the numbers is not a card", ErrJudge, name)
	}
	if !slices.Contains([]string{StageA, StageB, StageC}, c.Stage) {
		return fmt.Errorf("%w: %s is stage %q, which is not A, B or C", ErrJudge, name, c.Stage)
	}
	if c.Suite == "" {
		return fmt.Errorf("%w: %s names no suite", ErrJudge, name)
	}
	if len(c.Manifests) == 0 {
		return fmt.Errorf("%w: %s names no item manifest", ErrJudge, name)
	}
	if c.Base.ID == "" || c.Candidate.ID == "" {
		return fmt.Errorf("%w: %s leaves a condition unnamed", ErrJudge, name)
	}
	if c.Base.ID == c.Candidate.ID {
		return fmt.Errorf("%w: %s calls both conditions %q; a comparison of one condition "+
			"with itself still needs two identifiers", ErrJudge, name, c.Base.ID)
	}
	if c.Base.Config == "" || c.Candidate.Config == "" {
		return fmt.Errorf("%w: %s leaves a condition with no configuration", ErrJudge, name)
	}
	if !slices.Contains([]string{RuleSpeed, RuleQuality, RuleOutputFix}, c.Rules.Kind) {
		return fmt.Errorf("%w: %s has rule kind %q; a card says whether it is buying speed "+
			"(%q), quality (%q) or a repair (%q)",
			ErrJudge, name, c.Rules.Kind, RuleSpeed, RuleQuality, RuleOutputFix)
	}
	if c.Reuse.Kind == "" {
		c.Reuse.Kind = ReuseNone
	}
	if !slices.Contains([]string{ReuseNone, ReuseSavedRuns, ReuseCalibration, ReuseTrial}, c.Reuse.Kind) {
		return fmt.Errorf("%w: %s reuses %q, which is not one of %s, %s, %s, %s",
			ErrJudge, name, c.Reuse.Kind, ReuseNone, ReuseSavedRuns, ReuseCalibration, ReuseTrial)
	}
	if c.Reuse.Kind != ReuseNone && c.Reuse.Source == "" {
		return fmt.Errorf("%w: %s reuses %q and names no source", ErrJudge, name, c.Reuse.Kind)
	}
	if c.BudgetSeconds == 0 {
		c.BudgetSeconds = defaultBudget(c.Stage)
	}
	if c.BudgetSeconds < 0 {
		return fmt.Errorf("%w: %s asks for a budget of %d seconds", ErrJudge, name, c.BudgetSeconds)
	}
	if c.BlockSize <= 0 {
		c.BlockSize = 4
	}
	if c.MinItemsPerCategory <= 0 {
		c.MinItemsPerCategory = DefaultMinItemsPerCategory
	}
	return nil
}

func defaultBudget(stage string) int {
	switch stage {
	case StageA:
		return BudgetA
	case StageB:
		return BudgetB
	}
	return BudgetC
}

// Path resolves a path the card named, against the card's own directory.
func (c TrialCard) Path(name string) string {
	if name == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(c.Dir, filepath.FromSlash(name))
}

// PathsOf resolves a list of paths the card named.
func (c TrialCard) PathsOf(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, c.Path(name))
	}
	return out
}

// TrialManifest is one item set: which items, what strata they carry, and what
// share of the target population each stratum stands for.
type TrialManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Set           string              `json:"set"`
	Items         []TrialManifestItem `json:"items"`
	// Weights are the target shares, keyed by stratum value. They are
	// optional: a set built by hand for a stage A probe has no population to
	// stand for, and demanding a weight would make the honest case fail.
	Weights map[string]float64 `json:"weights,omitempty"`
	// Path is where the manifest was read from.
	Path string `json:"-"`
}

// TrialManifestItem is one item of a set.
type TrialManifestItem struct {
	ID     string            `json:"id"`
	Strata map[string]string `json:"strata,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

// LoadTrialManifest reads and validates one item manifest.
func LoadTrialManifest(name string) (TrialManifest, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the manifest
	if err != nil {
		return TrialManifest{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var m TrialManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return TrialManifest{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	m.Path = name
	if m.SchemaVersion != TrialSchemaVersion {
		return TrialManifest{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrJudge, name, m.SchemaVersion, TrialSchemaVersion)
	}
	if !slices.Contains([]string{SetD, SetR, SetH}, m.Set) {
		return TrialManifest{}, fmt.Errorf("%w: %s is set %q, which is not %s, %s or %s",
			ErrJudge, name, m.Set, SetD, SetR, SetH)
	}
	if len(m.Items) == 0 {
		return TrialManifest{}, fmt.Errorf("%w: %s holds no items", ErrJudge, name)
	}
	seen := map[string]bool{}
	for i, item := range m.Items {
		if item.ID == "" {
			return TrialManifest{}, fmt.Errorf("%w: %s: item %d has no id", ErrJudge, name, i)
		}
		if seen[item.ID] {
			return TrialManifest{}, fmt.Errorf("%w: %s names item %s twice", ErrJudge, name, item.ID)
		}
		seen[item.ID] = true
	}
	for stratum, share := range m.Weights {
		if share < 0 {
			return TrialManifest{}, fmt.Errorf("%w: %s weights stratum %q at %v",
				ErrJudge, name, stratum, share)
		}
	}
	for _, item := range m.Items {
		if _, err := m.Bucket(item); err != nil {
			return TrialManifest{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
		}
	}
	return m, nil
}

// Bucket is the weight bucket one item falls in: the stratum value of the item
// that the manifest gives a target share to.
//
// An item matching no weight is unweighted and answers the empty string, which
// is the ordinary case for a manifest with no weights at all. An item matching
// two is an error rather than a choice: which of the two shares it counted
// towards would decide the weighted mean, and nothing in the file says.
func (m TrialManifest) Bucket(item TrialManifestItem) (string, error) {
	if len(m.Weights) == 0 {
		return "", nil
	}
	var found []string
	for _, key := range sortedKeys(item.Strata) {
		if _, ok := m.Weights[item.Strata[key]]; ok {
			found = append(found, item.Strata[key])
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("item %s carries %d weighted strata (%s); a weight key names one "+
		"stratum value and an item belongs to one bucket",
		item.ID, len(found), strings.Join(found, ", "))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ReuseKey is everything a judge's answer to one item depends on.
//
// Two runs with the same key answer the same question and one may stand in for
// the other. Two runs with a different key do not, however similar they look,
// and the field that differs is what a report prints instead of a number.
type ReuseKey struct {
	// The item, as the corpus holds it now.
	Task string `json:"task_sha256"`
	// Conversation is the harness's own digest of the conversation, taken
	// from a run rather than computed here. The harness hashes the canonical
	// encoding of the decoded conversation and not the bytes of the file, so
	// a digest computed from the file would never equal a recorded one and
	// every saved run would be rejected for the wrong reason. What this field
	// compares is the recorded digest of a saved run against the recorded
	// digest of the run this trial just made for the same item.
	Conversation string `json:"conversation_sha256"`
	Rubric       string `json:"rubric_sha256,omitempty"`
	Reference    string `json:"reference_sha256,omitempty"`
	// Candidates covers both the answer texts and which identifier each of
	// them was given: it is the digest of `c1:<sha>\n c2:<sha>\n …`, so
	// swapping two candidates between positions changes it.
	Candidates string `json:"candidates_sha256"`
	// The harness that produced the answer.
	PromptVersion string `json:"prompt_version"`
	CMoAVersion   string `json:"cmoa_version"`
	SelectionRule string `json:"selection_rule"`
	// The judge's settings, as a digest over model, base URL, temperature,
	// token budget, output format and parallelism. The sampling seed is
	// carried separately so a run that differs only in its seed says so.
	Judge     string `json:"judge_sha256"`
	AllowTie  bool   `json:"allow_tie"`
	Seed      int    `json:"seed"`
	JudgeSeed int    `json:"judge_seed"`
}

// Digest is the whole key as one hash, for a report to quote.
func (k ReuseKey) Digest() string {
	body, err := json.Marshal(k)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Diff names the fields two keys disagree about, in a fixed order.
func (k ReuseKey) Diff(other ReuseKey) []string {
	var out []string
	add := func(name string, same bool) {
		if !same {
			out = append(out, name)
		}
	}
	add("task", k.Task == other.Task)
	add("conversation", k.Conversation == other.Conversation)
	add("rubric", k.Rubric == other.Rubric)
	add("reference", k.Reference == other.Reference)
	add("candidates", k.Candidates == other.Candidates)
	add("prompt_version", k.PromptVersion == other.PromptVersion)
	add("cmoa_version", k.CMoAVersion == other.CMoAVersion)
	add("selection_rule", k.SelectionRule == other.SelectionRule)
	add("judge", k.Judge == other.Judge)
	add("allow_tie", k.AllowTie == other.AllowTie)
	add("seed", k.Seed == other.Seed)
	add("judge_seed", k.JudgeSeed == other.JudgeSeed)
	return out
}

// judgeDigestKeys are the judge settings that go into the key.
//
// They are the ones a harness configuration and a written trace both carry, so
// the expected key of a condition that has not run yet can be computed from
// its configuration file and compared with a run that already exists. The
// sampling seed is left out on purpose — it is its own field — and so is the
// timeout, which changes what happens when the fleet is slow and not what the
// judge is asked.
var judgeDigestKeys = []string{
	"model", "base_url", "temperature", "max_tokens", "output_format", "parallel", "extra_body",
}

// judgeDigest hashes the judge settings of a configuration or a trace.
func judgeDigest(judge map[string]json.RawMessage) string {
	picked := map[string]json.RawMessage{}
	for _, key := range judgeDigestKeys {
		if value, ok := judge[key]; ok {
			picked[key] = value
		}
	}
	// encoding/json writes a map's keys in sorted order, so this is canonical
	// without a hand-written encoder.
	body, err := json.Marshal(picked)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// fileDigest hashes one file. A file that is not there hashes to the empty
// string rather than failing: a rubric is optional, and "there is no rubric"
// is a state the key has to be able to record.
func fileDigest(name string) (string, error) {
	body, err := os.ReadFile(name) //nolint:gosec // a path inside the corpus
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrJudge, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// taskFile is the part of an item's task.json a trial reads.
type taskFile struct {
	Conversation string `json:"conversation"`
	Rubric       string `json:"rubric"`
	Reference    string `json:"reference"`
	Judge        struct {
		AllowTie *bool `json:"allow_tie"`
	} `json:"judge"`
}

// CorpusKey is the half of the reuse key that comes from the item on disk.
//
// The runtime half — prompt version, harness build, selection rule — is only
// knowable from a run, which is why it is filled in later from the first run
// the trial makes. What this part says is that both conditions were shown the
// same question and the same three answers under the same three names.
func CorpusKey(taskDir string, candidates []string) (ReuseKey, error) {
	var key ReuseKey
	body, err := os.ReadFile(filepath.Join(taskDir, "task.json")) //nolint:gosec // a path inside the corpus
	if err != nil {
		return key, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	sum := sha256.Sum256(body)
	key.Task = hex.EncodeToString(sum[:])
	var task taskFile
	if err := json.Unmarshal(body, &task); err != nil {
		return key, fmt.Errorf("%w: %s/task.json: %w", ErrJudge, taskDir, err)
	}
	key.AllowTie = task.Judge.AllowTie == nil || *task.Judge.AllowTie
	// The conversation is deliberately absent: its digest is the harness's,
	// over the decoded conversation rather than the file, and computing one
	// here would only produce a number that never matches.
	for _, pair := range []struct {
		name string
		into *string
	}{
		{task.Rubric, &key.Rubric},
		{task.Reference, &key.Reference},
	} {
		if pair.name == "" {
			continue
		}
		digest, err := fileDigest(filepath.Join(taskDir, filepath.FromSlash(pair.name)))
		if err != nil {
			return key, err
		}
		*pair.into = digest
	}
	var lines strings.Builder
	for i, name := range candidates {
		digest, err := fileDigest(name)
		if err != nil {
			return key, err
		}
		if digest == "" {
			return key, fmt.Errorf("%w: %s has no candidate answer at position %s",
				ErrJudge, taskDir, Positions[min(i, len(Positions)-1)])
		}
		fmt.Fprintf(&lines, "%s:%s\n", Positions[min(i, len(Positions)-1)], digest)
	}
	sum = sha256.Sum256([]byte(lines.String()))
	key.Candidates = hex.EncodeToString(sum[:])
	return key, nil
}

// runFile, judgeKeyFile and selectFile are the parts of a trace the key reads.
type runFile struct {
	PromptVersion string `json:"prompt_version"`
	CMoAVersion   string `json:"cmoa_version"`
	Conversation  string `json:"conversation_sha256"`
	External      []struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	} `json:"external_candidates"`
}

type judgeKeyFile struct {
	Judge        map[string]json.RawMessage `json:"judge"`
	Presentation struct {
		Seed int `json:"seed"`
	} `json:"presentation"`
}

type selectFile struct {
	Rule string `json:"rule"`
}

// KeyOfRun reads the reuse key a saved run answers under.
//
// The item half is read out of the run's own record rather than off today's
// disk, which is the point: a corpus that changed after the run was made has
// to make the key differ, and it can only do that if the run remembers what it
// saw. What the run does not record — the rubric and the task file itself —
// is filled in by the caller from the corpus, and the report says so.
func KeyOfRun(dir string) (ReuseKey, error) {
	var key ReuseKey
	var run runFile
	if err := readJSONFile(filepath.Join(dir, "run.json"), &run); err != nil {
		return key, err
	}
	var judged judgeKeyFile
	if err := readJSONFile(filepath.Join(dir, JudgeFile), &judged); err != nil {
		return key, err
	}
	var selected selectFile
	if err := readJSONFile(filepath.Join(dir, "select.json"), &selected); err != nil {
		return key, err
	}
	key.Conversation = run.Conversation
	key.PromptVersion = run.PromptVersion
	key.CMoAVersion = run.CMoAVersion
	key.SelectionRule = selected.Rule
	key.Judge = judgeDigest(judged.Judge)
	key.Seed = judged.Presentation.Seed
	if raw, ok := judged.Judge["seed"]; ok {
		_ = json.Unmarshal(raw, &key.JudgeSeed)
	}
	key.AllowTie = true
	if raw, ok := judged.Judge["allow_tie"]; ok {
		_ = json.Unmarshal(raw, &key.AllowTie)
	}
	var lines strings.Builder
	for _, candidate := range run.External {
		fmt.Fprintf(&lines, "%s:%s\n", candidate.ID, candidate.SHA256)
	}
	sum := sha256.Sum256([]byte(lines.String()))
	key.Candidates = hex.EncodeToString(sum[:])
	return key, nil
}

func readJSONFile(name string, into any) error {
	body, err := os.ReadFile(name) //nolint:gosec // a path inside a run directory
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	return nil
}

// configJudge reads the judge block out of a harness configuration.
func configJudge(name string) (map[string]json.RawMessage, error) {
	var config struct {
		Judge map[string]json.RawMessage `json:"judge"`
	}
	if err := readJSONFile(name, &config); err != nil {
		return nil, err
	}
	if config.Judge == nil {
		return nil, fmt.Errorf("%w: %s declares no judge", ErrJudge, name)
	}
	return config.Judge, nil
}

// ConfigDigest hashes a harness configuration file whole, for the card to pin.
func ConfigDigest(name string) (string, error) {
	digest, err := fileDigest(name)
	if err != nil {
		return "", err
	}
	if digest == "" {
		return "", fmt.Errorf("%w: %s is not there", ErrJudge, name)
	}
	return digest, nil
}

// TrialRunner asks the harness one question. It is an interface so the
// arithmetic above it is testable without a fleet.
type TrialRunner interface {
	// Judge runs one item under one configuration and answers with what the
	// judge concluded, where the trace went, and the wall clock the call took.
	Judge(ctx context.Context, taskDir string, candidates []string,
		config string, seed, judgeSeed int) (Judged, time.Duration, error)
}

// CMoATrialRunner runs the judge by asking the harness binary to do it.
type CMoATrialRunner struct {
	Binary string
	Log    func(string)
}

// Judge shells out to `cmoa judge` and reads the trace it left.
func (r CMoATrialRunner) Judge(ctx context.Context, taskDir string, candidates []string,
	config string, seed, judgeSeed int,
) (Judged, time.Duration, error) {
	args := []string{"judge", "--task", taskDir}
	for _, candidate := range candidates {
		args = append(args, "--candidate", candidate)
	}
	args = append(args, "--config", config, "--seed", strconv.Itoa(seed))
	if judgeSeed != 0 {
		args = append(args, "--judge-seed", strconv.Itoa(judgeSeed))
	}
	cmd := exec.CommandContext(ctx, r.Binary, args...) //nolint:gosec // the caller names the harness
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	started := time.Now()
	err := cmd.Run()
	elapsed := time.Since(started)
	if err != nil {
		return Judged{}, elapsed, fmt.Errorf("%w: %s: %w: %s",
			ErrJudge, filepath.Base(taskDir), err, firstLine(errOut.String()))
	}
	dir, err := runDir(out.String())
	if err != nil {
		return Judged{}, elapsed, fmt.Errorf("%w: %s: %w", ErrJudge, filepath.Base(taskDir), err)
	}
	judged, err := ReadJudged(dir)
	if err != nil {
		return Judged{}, elapsed, err
	}
	judged.Seed = seed
	if r.Log != nil {
		r.Log(fmt.Sprintf("judge %s seed=%d -> %s (%.1fs)",
			filepath.Base(taskDir), seed, judged.Outcome, elapsed.Seconds()))
	}
	return judged, elapsed, nil
}

// TrialStep is one item under one condition: the unit the plan is made of and
// the unit a resume skips.
type TrialStep struct {
	Item      string `json:"item"`
	Set       string `json:"set"`
	Condition string `json:"condition"`
}

// TrialRecord is one completed step, as the results journal holds it.
type TrialRecord struct {
	Item        string `json:"item"`
	Set         string `json:"set"`
	Condition   string `json:"condition"`
	ConditionID string `json:"condition_id"`
	// Source is measured or reused.
	Source string `json:"source"`
	// ReuseKey is the digest the step ran or was read under.
	ReuseKey string `json:"reuse_key"`
	// RunDir is the trace, relative to the vault where it can be.
	RunDir string `json:"run_dir,omitempty"`
	// What the judge concluded.
	Outcome   string `json:"outcome"`
	Candidate string `json:"candidate,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Category  string `json:"category,omitempty"`
	Measured  bool   `json:"measured"`
	// The costs. WallSeconds is nil for a reused answer, and nil is the whole
	// point: an old wall time is not this trial's wall time.
	Calls          int      `json:"calls"`
	InvalidRetries int      `json:"invalid_output_retries"`
	SwapConsistent int      `json:"swap_consistent_pairs"`
	LatencyMS      int64    `json:"latency_ms"`
	WallSeconds    *float64 `json:"wall_seconds"`
	// Error is what went wrong, where something did.
	Error string `json:"error,omitempty"`
	At    string `json:"at"`
}

// Key identifies the step a record completes.
func (r TrialRecord) Key() TrialStep {
	return TrialStep{Item: r.Item, Set: r.Set, Condition: r.Condition}
}

// TrialOptions are one trial run.
type TrialOptions struct {
	Card      TrialCard
	Suite     Suite
	Manifests []TrialManifest
	Labels    []Label
	Runner    TrialRunner
	// Out is where the journal and the report go.
	Out string
	// Resume continues an interrupted run: every step already in the journal
	// is skipped and never asked again.
	Resume bool
	// Vault is what a recorded trace path is written relative to.
	Vault string
	// Now is the clock, injectable so a budget is testable in milliseconds.
	Now func() time.Time
	// Log receives one line per step.
	Log func(string)
}

// TrialResult is a finished trial.
type TrialResult struct {
	Report  TrialReport
	Records []TrialRecord
}

// Plan is the fixed order of steps.
//
// The order is the manifests' order, then each manifest's item order, and it
// does not depend on anything measured. Within an item the candidate condition
// goes first, which is not arbitrary: the runtime half of the reuse key is
// only knowable from a run the harness has actually made, so the first
// candidate run is what tells the trial which saved base runs are reusable.
//
// A card asking for alternating blocks gets the same steps grouped differently:
// blocks of BlockSize items, each block running one condition through and then
// the other, with the leading condition swapping block by block. That is the
// order for a change to the runtime, where running one condition to the end
// and then the other would confound the change with the hour.
func (o TrialOptions) Plan() []TrialStep {
	var items []TrialStep
	for _, manifest := range o.Manifests {
		for _, item := range manifest.Items {
			items = append(items, TrialStep{Item: item.ID, Set: manifest.Set})
		}
	}
	var plan []TrialStep
	with := func(step TrialStep, condition string) TrialStep {
		step.Condition = condition
		return step
	}
	if !o.Card.AlternatingBlocks {
		for _, item := range items {
			plan = append(plan, with(item, ConditionCandidate), with(item, ConditionBase))
		}
		return plan
	}
	size := o.Card.BlockSize
	for start, block := 0, 0; start < len(items); start, block = start+size, block+1 {
		end := min(start+size, len(items))
		first, second := ConditionCandidate, ConditionBase
		if block%2 == 1 {
			first, second = second, first
		}
		for _, condition := range []string{first, second} {
			for _, item := range items[start:end] {
				plan = append(plan, with(item, condition))
			}
		}
	}
	return plan
}

// ReadTrialRecords reads a results journal back.
func ReadTrialRecords(name string) ([]TrialRecord, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the output directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var out []TrialRecord
	for i, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record TrialRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("%w: %s:%d: %w", ErrJudge, name, i+1, err)
		}
		out = append(out, record)
	}
	return out, nil
}

// Trial runs the plan and returns the comparison.
func Trial(ctx context.Context, opts TrialOptions) (TrialResult, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}

	journal := filepath.Join(opts.Out, TrialResultsFile)
	done, err := ReadTrialRecords(journal)
	if err != nil {
		return TrialResult{}, err
	}
	if len(done) > 0 && !opts.Resume {
		return TrialResult{}, fmt.Errorf(
			"%w: %s already holds %d completed step(s); pass --resume to continue it, "+
				"or name another --out. A trial never re-runs a step it has recorded",
			ErrJudge, journal, len(done))
	}
	completed := map[TrialStep]bool{}
	for _, record := range done {
		completed[record.Key()] = true
	}

	run := &trialRun{
		opts: opts, now: now, log: log, journal: journal,
		records: done, tasks: map[string]Task{}, corpus: map[string]ReuseKey{},
		conversations: map[string]string{},
	}
	for _, task := range opts.Suite.Tasks {
		run.tasks[task.ID] = task
	}
	if err := run.openJournal(); err != nil {
		return TrialResult{}, err
	}
	defer run.closeJournal()

	loaded := now()
	if err := run.reuseIndex(); err != nil {
		return TrialResult{}, err
	}
	run.loadSeconds = loaded.Sub(started).Seconds()

	plan := opts.Plan()
	deadline := started.Add(time.Duration(opts.Card.BudgetSeconds) * time.Second)
	for _, step := range plan {
		if completed[step] {
			run.skipped++
			continue
		}
		if err := run.step(ctx, step, deadline); err != nil {
			if errors.Is(err, errBudget) {
				run.overBudget = true
				continue
			}
			if errors.Is(err, ctx.Err()) && ctx.Err() != nil {
				return TrialResult{}, err
			}
			// A step that failed is recorded as a failure and the trial goes
			// on: one item the harness could not answer is a line in the
			// report, and stopping the whole comparison for it would lose the
			// items that did answer.
			run.record(TrialRecord{
				Item: step.Item, Set: step.Set, Condition: step.Condition,
				ConditionID: run.conditionID(step.Condition), Source: SourceMeasured,
				Outcome: "", Error: scrub(err.Error(), opts.Vault, opts.Suite.Dir),
				At: run.now().UTC().Format(time.RFC3339),
			})
			log(fmt.Sprintf("%s %s: %v", step.Condition, step.Item, err))
		}
	}
	measured := now()
	report := run.assemble(plan, started, measured)
	report.Phases.AggregateSeconds = now().Sub(measured).Seconds()
	report.TEvalSeconds = now().Sub(started).Seconds()
	return TrialResult{Report: report, Records: run.records}, nil
}

// errBudget is the time box refusing to launch more work. It is not a failure
// of the change under test and it never reads as one.
var errBudget = errors.New("the time budget stopped new work")

// trialRun is the state of one pass over the plan.
type trialRun struct {
	opts    TrialOptions
	now     func() time.Time
	log     func(string)
	journal string
	file    *os.File
	records []TrialRecord

	tasks  map[string]Task
	corpus map[string]ReuseKey
	// conversations is, per item, the conversation digest the harness itself
	// wrote for a run this trial made. It is what a saved run's recorded
	// digest is compared against, and an item with none yet is an item whose
	// saved runs cannot be checked — so they are not reused.
	conversations map[string]string
	// reusable is, per item, the saved runs offered as the base condition.
	reusable map[string][]string
	// expected is the base condition's key once the runtime half is known,
	// and resolved says whether it is.
	expected ReuseKey
	resolved bool
	// mismatch is why the first rejected reuse was rejected, for the report.
	mismatch []string

	loadSeconds    float64
	measureSeconds float64
	skipped        int
	overBudget     bool
}

func (r *trialRun) openJournal() error {
	if err := os.MkdirAll(r.opts.Out, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	file, err := os.OpenFile(r.journal, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // a journal is world-readable
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	r.file = file
	return nil
}

func (r *trialRun) closeJournal() {
	if r.file != nil {
		_ = r.file.Close()
	}
}

func (r *trialRun) conditionID(condition string) string {
	if condition == ConditionBase {
		return r.opts.Card.Base.ID
	}
	return r.opts.Card.Candidate.ID
}

func (r *trialRun) config(condition string) string {
	if condition == ConditionBase {
		return r.opts.Card.Path(r.opts.Card.Base.Config)
	}
	return r.opts.Card.Path(r.opts.Card.Candidate.Config)
}

// reuseIndex collects, per item, the saved runs the card offers as the base.
func (r *trialRun) reuseIndex() error {
	r.reusable = map[string][]string{}
	card := r.opts.Card
	source := card.Path(card.Reuse.Source)
	switch card.Reuse.Kind {
	case ReuseNone:
		return nil
	case ReuseSavedRuns:
		for _, manifest := range r.opts.Manifests {
			for _, item := range manifest.Items {
				task, ok := r.tasks[item.ID]
				if !ok {
					continue
				}
				dirs, err := filepath.Glob(filepath.Join(source, filepath.FromSlash(task.Dir), "runs", "*"))
				if err != nil {
					return fmt.Errorf("%w: %w", ErrJudge, err)
				}
				sort.Strings(dirs)
				r.reusable[item.ID] = dirs
			}
		}
	case ReuseCalibration:
		var items []ItemResult
		body, err := os.ReadFile(filepath.Join(source, ItemsFile)) //nolint:gosec // the card names the source
		if err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var item ItemResult
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				return fmt.Errorf("%w: %s/%s: %w", ErrJudge, source, ItemsFile, err)
			}
			items = append(items, item)
		}
		for _, item := range items {
			for _, run := range item.Runs {
				if run.RunDir != "" {
					r.reusable[item.Item] = append(r.reusable[item.Item],
						filepath.Join(r.opts.Vault, filepath.FromSlash(run.RunDir)))
				}
			}
		}
	case ReuseTrial:
		records, err := ReadTrialRecords(filepath.Join(source, TrialResultsFile))
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.RunDir != "" {
				r.reusable[record.Item] = append(r.reusable[record.Item],
					filepath.Join(r.opts.Vault, filepath.FromSlash(record.RunDir)))
			}
		}
	}
	return nil
}

// corpusKey is the item half of the key, computed once per item.
func (r *trialRun) corpusKey(task Task) (ReuseKey, error) {
	if key, ok := r.corpus[task.ID]; ok {
		return key, nil
	}
	key, err := CorpusKey(r.opts.Suite.TaskDir(task), r.opts.Suite.Candidates(task))
	if err != nil {
		return key, err
	}
	r.corpus[task.ID] = key
	return key, nil
}

// step runs or reuses one item under one condition.
func (r *trialRun) step(ctx context.Context, step TrialStep, deadline time.Time) error {
	task, ok := r.tasks[step.Item]
	if !ok {
		return fmt.Errorf("%w: the suite holds no item %s", ErrJudge, step.Item)
	}
	corpus, err := r.corpusKey(task)
	if err != nil {
		return err
	}
	if step.Condition == ConditionBase && r.resolved {
		if dir, key := r.reuse(step.Item, corpus); dir != "" {
			return r.reuseStep(step, dir, key)
		}
	}
	// Only work that costs an inference is gated by the box. A reused answer
	// costs nothing, and stopping it would leave items with one condition
	// measured and the other missing — the one shape a comparison cannot use.
	if r.now().After(deadline) {
		return errBudget
	}
	judged, elapsed, err := r.opts.Runner.Judge(ctx, r.opts.Suite.TaskDir(task),
		r.opts.Suite.Candidates(task), r.config(step.Condition),
		r.opts.Card.Seed, r.opts.Card.JudgeSeed)
	r.measureSeconds += elapsed.Seconds()
	if err != nil {
		return err
	}
	seconds := elapsed.Seconds()
	key := corpus
	if produced, err := KeyOfRun(judged.RunDir); err == nil {
		key.Conversation = produced.Conversation
		r.conversations[step.Item] = produced.Conversation
		key.PromptVersion = produced.PromptVersion
		key.CMoAVersion = produced.CMoAVersion
		key.SelectionRule = produced.SelectionRule
		key.Judge = produced.Judge
		key.Seed = produced.Seed
		key.JudgeSeed = produced.JudgeSeed
		if step.Condition == ConditionCandidate && !r.resolved {
			r.resolve(produced)
		}
	}
	r.record(TrialRecord{
		Item: step.Item, Set: step.Set, Condition: step.Condition,
		ConditionID: r.conditionID(step.Condition), Source: SourceMeasured,
		ReuseKey: key.Digest(),
		RunDir:   RecordPath(judged.RunDir, r.opts.Vault, r.opts.Suite.Dir),
		Outcome:  judged.Outcome, Candidate: judged.Candidate, Reason: judged.Reason,
		Category: answerCategory(judged), Measured: judged.Measured(),
		Calls: callsOf(judged), InvalidRetries: judged.InvalidRetries,
		SwapConsistent: judged.SwapConsistent, LatencyMS: judged.LatencyMS,
		WallSeconds: &seconds, At: r.now().UTC().Format(time.RFC3339),
	})
	return nil
}

// resolve fills in the runtime half of the base condition's expected key from
// the first run the harness actually made.
//
// The judge settings come from the base condition's own configuration file;
// everything else — prompt version, harness build, selection rule — comes from
// the produced run, because those are properties of the binary rather than of
// the configuration and there is nowhere else to read them. Where the change
// under test is a change to the prompt, the base's saved runs then differ in
// prompt_version and are all rejected. That is the safe direction: the trial
// measures the base rather than reusing a run made under a different prompt.
func (r *trialRun) resolve(produced ReuseKey) {
	r.expected.PromptVersion = produced.PromptVersion
	r.expected.CMoAVersion = produced.CMoAVersion
	r.expected.SelectionRule = produced.SelectionRule
	r.expected.Seed = r.opts.Card.Seed
	r.expected.JudgeSeed = r.opts.Card.JudgeSeed
	judge, err := configJudge(r.config(ConditionBase))
	if err != nil {
		r.log(fmt.Sprintf("base configuration unreadable (%v); the base will be measured", err))
		return
	}
	r.expected.Judge = judgeDigest(judge)
	if r.expected.JudgeSeed == 0 {
		if raw, ok := judge["seed"]; ok {
			_ = json.Unmarshal(raw, &r.expected.JudgeSeed)
		}
	}
	r.resolved = true
}

// reuse finds a saved run for the item whose key matches the base condition's.
func (r *trialRun) reuse(item string, corpus ReuseKey) (string, ReuseKey) {
	want := r.expected
	want.Task = corpus.Task
	want.Conversation = r.conversations[item]
	want.Rubric = corpus.Rubric
	want.Reference = corpus.Reference
	want.Candidates = corpus.Candidates
	want.AllowTie = corpus.AllowTie
	for _, dir := range r.reusable[item] {
		got, err := KeyOfRun(dir)
		if err != nil {
			continue
		}
		// The rubric, the reference and the task file are not in a trace, so
		// the corpus's answer stands for both sides: what they say is that the
		// two conditions were given the same inputs, not that the saved run
		// saw them. The conversation is checked properly, because the harness
		// records its digest and this trial has one of its own to compare.
		got.Task, got.Rubric, got.Reference = want.Task, want.Rubric, want.Reference
		if diff := want.Diff(got); len(diff) == 0 {
			return dir, want
		} else if r.mismatch == nil {
			r.mismatch = diff
		}
	}
	return "", want
}

func (r *trialRun) reuseStep(step TrialStep, dir string, key ReuseKey) error {
	judged, err := ReadJudged(dir)
	if err != nil {
		return err
	}
	r.record(TrialRecord{
		Item: step.Item, Set: step.Set, Condition: step.Condition,
		ConditionID: r.conditionID(step.Condition), Source: SourceReused,
		ReuseKey: key.Digest(), RunDir: RecordPath(dir, r.opts.Vault, r.opts.Suite.Dir),
		Outcome: judged.Outcome, Candidate: judged.Candidate, Reason: judged.Reason,
		Category: answerCategory(judged), Measured: judged.Measured(),
		Calls: callsOf(judged), InvalidRetries: judged.InvalidRetries,
		SwapConsistent: judged.SwapConsistent, LatencyMS: judged.LatencyMS,
		WallSeconds: nil, At: r.now().UTC().Format(time.RFC3339),
	})
	return nil
}

// callsOf is how many judge calls a run made.
//
// It is zero for a run the candidates settled between themselves, which is the
// point of the stage that settles them: agreement between proposers is
// evidence, and it is the only evidence in the system that costs nothing.
func callsOf(j Judged) int {
	n := 0
	for _, pair := range j.Pairs {
		n += len(pair.Orders)
	}
	return n
}

// answerCategory is the run's answer as a label category, empty where the run
// measured nothing.
func answerCategory(j Judged) string {
	if !j.Measured() {
		return ""
	}
	return j.Category()
}

// record appends one completed step to the journal and to memory. The journal
// is flushed per line: a trial that is killed keeps every step it finished.
func (r *trialRun) record(record TrialRecord) {
	r.records = append(r.records, record)
	if r.file == nil {
		return
	}
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = r.file.Write(append(line, '\n'))
	_ = r.file.Sync()
}
