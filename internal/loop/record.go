package loop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// The three files a run leaves behind. Nothing in any of them names the
// machine the run happened on: an absolute path, a host name or a container
// name would carry somebody's home directory into a record that is meant to be
// committed.
const (
	// HeaderName is the run's parameters, renders and verdicts.
	HeaderName = "run.json"
	// TrialsName is the append-only per-trial journal, one JSON object a line.
	TrialsName = "trials.jsonl"
	// AAName is the calibration, where one was run.
	AAName = "aa.json"
)

// SuiteRef names the task set a run measured over.
type SuiteRef struct {
	ID     string      `json:"id"`
	Digest string      `json:"split_sha256"`
	Floor  int         `json:"min_tasks_per_split"`
	Sizes  []SplitSize `json:"sizes"`
}

// SplitSize is how many tasks one split holds.
type SplitSize struct {
	Split string `json:"split"`
	Tasks int    `json:"tasks"`
}

// RenderRef is one arm's harness, as the record names it.
type RenderRef struct {
	AsOf            string   `json:"as_of"`
	At              string   `json:"at"`
	TreeSHA256      string   `json:"tree_sha256"`
	SeedSHA256      string   `json:"seed_sha256"`
	RendererVersion string   `json:"renderer_version"`
	DocDagVersion   string   `json:"docdag_version"`
	OrderKey        string   `json:"order_key"`
	Edits           []string `json:"edits"`
	Files           int      `json:"files"`
}

// summarise reduces a manifest to what the run header carries. The manifest
// itself is written beside the rendered tree; repeating every file digest here
// would make the header unreadable without saying anything new.
func summarise(m render.Manifest) RenderRef {
	ref := RenderRef{
		AsOf: m.AsOf, At: m.At, TreeSHA256: m.TreeSHA256, SeedSHA256: m.SeedSHA256,
		RendererVersion: m.RendererVersion, DocDagVersion: m.DocDagVersion,
		OrderKey: m.OrderKey, Files: len(m.Files),
	}
	for _, edit := range m.Edits {
		ref.Edits = append(ref.Edits, edit.ID)
	}
	return ref
}

// PreRegistration is where the claim was written down before the run was made,
// so a reader can check the order rather than take it on trust.
type PreRegistration struct {
	EditPath string   `json:"edit_path"`
	Commit   string   `json:"commit"`
	Predicts []string `json:"predicts"`
}

// SplitVerdict is one split's reading.
type SplitVerdict struct {
	Split   string        `json:"split"`
	Reading stats.Reading `json:"reading"`
}

// Transition is what the run did to the edit's own document afterwards.
type Transition struct {
	// Status is the status the edit ends the run in.
	Status string `json:"status"`
	// Approval is the approval word written with it.
	Approval string `json:"approval,omitempty"`
	// Applied says the run wrote the document. It is false where the surface
	// needs a person: the run says what it found and leaves the decision.
	Applied bool `json:"applied"`
	// Autonomy is the surface's autonomy word, which is why.
	Autonomy string `json:"autonomy"`
	// Reason is the sentence a reader wants.
	Reason string `json:"reason"`
	// Instruction is what a person has to do, where anything is left to do.
	Instruction string `json:"instruction,omitempty"`
}

// Header is run.json: everything a reader needs to know what was measured and
// under what rule, and everything the replay needs to recompute the verdicts.
type Header struct {
	SchemaVersion  int    `json:"schema_version"`
	UzushioVersion string `json:"uzushio_version"`
	// RunID is the run directory's own name.
	RunID      string   `json:"run_id"`
	Edit       string   `json:"edit"`
	EditStatus string   `json:"edit_status"`
	Component  string   `json:"component"`
	Mode       Mode     `json:"mode"`
	AsOf       string   `json:"as_of"`
	DryRun     bool     `json:"dry_run,omitempty"`
	Suite      SuiteRef `json:"suite"`
	Repeats    int      `json:"repeats"`
	Parallel   int      `json:"parallel"`
	Pool       string   `json:"pool"`
	Proposers  []string `json:"proposers"`
	// FleetSHA256 identifies the pool by what it is rather than by what it is
	// called: the identifier, model and endpoint of each proposer, in
	// configured order. It keys the baseline cache. No secret goes into it.
	FleetSHA256     string                  `json:"fleet_sha256"`
	Params          map[string]stats.Params `json:"params"`
	DecisionRule    string                  `json:"decision_rule"`
	Baseline        RenderRef               `json:"baseline"`
	Candidate       RenderRef               `json:"candidate"`
	PreRegistration PreRegistration         `json:"pre_registration"`
	AADiscordance   *float64                `json:"aa_discordance,omitempty"`
	AAWarning       string                  `json:"aa_warning,omitempty"`
	Unmeasurable    *Unmeasurable           `json:"unmeasurable,omitempty"`
	Verdicts        []SplitVerdict          `json:"verdicts"`
	Promote         bool                    `json:"promote"`
	Transition      Transition              `json:"transition"`
	Documents       []string                `json:"documents,omitempty"`
	// Aborted says the run stopped without finishing, and why. A header with
	// one describes a journal that is real as far as it goes.
	Aborted    string `json:"aborted,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// Verdict reads one split's verdict out of a header.
func (h Header) Verdict(split vocab.Split) vocab.Verdict {
	for _, entry := range h.Verdicts {
		if entry.Split == split.String() {
			return entry.Reading.Verdict
		}
	}
	return vocab.VerdictInconclusive
}

// TrialRecord is one line of trials.jsonl: one trial, and the state of the
// arithmetic at the moment it was folded in.
//
// It carries the running statistic rather than only the outcome, so a reader
// can watch the e-process move without recomputing it, and so a run that was
// interrupted still shows where it had got to. The replay recomputes
// everything from the outcomes regardless — the stored numbers are for a
// person, and the invariant that they agree is what --replay checks.
type TrialRecord struct {
	SchemaVersion int     `json:"schema_version"`
	Edit          string  `json:"edit"`
	Split         string  `json:"split"`
	Task          string  `json:"task_id"`
	Repeat        int     `json:"repeat_index"`
	Seed          int64   `json:"seed"`
	Temperature   float64 `json:"temperature"`
	Pool          string  `json:"pool"`
	Arm           string  `json:"arm"`
	// HarnessSHA256 is the tree digest of the harness this arm read.
	HarnessSHA256 string `json:"harness_sha256"`
	SplitSHA256   string `json:"split_sha256"`
	FleetSHA256   string `json:"fleet_sha256"`
	PreregCommit  string `json:"prereg_commit"`
	// RunID names the directory this journal lives in, so a line that has
	// been copied out of it still says which run it came from.
	RunID string `json:"run_id"`

	Pass          bool   `json:"pass"`
	SelectionKind string `json:"selection_kind,omitempty"`
	ErrorClass    string `json:"error_class"`
	Error         string `json:"error,omitempty"`
	TraceRunID    string `json:"trace_run_id,omitempty"`
	WallMS        int64  `json:"wall_ms"`
	TokensIn      int    `json:"tokens_in"`
	TokensOut     int    `json:"tokens_out"`
	Candidates    int    `json:"candidates"`
	CacheHit      bool   `json:"baseline_cache_hit"`

	PairID      string `json:"pair_id"`
	PairOutcome string `json:"pair_outcome"`
	// TaskRepeats is how many pairs this split scheduled for this task. The
	// count gate asks whether a task's repeats are *all* in, and a journal
	// from a split that stopped early cannot answer that from its own row
	// count — so the schedule's answer is written down with every row.
	TaskRepeats int `json:"task_repeats"`

	CumWins   int            `json:"cum_b"`
	CumLosses int            `json:"cum_c"`
	CumTies   int            `json:"cum_ties"`
	LostTasks int            `json:"cum_lost_tasks"`
	LogEGain  stats.LogValue `json:"log_e_gain"`
	LogEHarm  stats.LogValue `json:"log_e_harm"`
	DeltaLo   float64        `json:"delta_lo"`
	DeltaHi   float64        `json:"delta_hi"`

	AllocReason string `json:"alloc_reason"`
	TS          string `json:"ts"`
}

// journal is the append-only per-trial record.
type journal struct {
	mu   sync.Mutex
	file *os.File
	out  *bufio.Writer
	now  func() time.Time
}

func openJournal(name string) (*journal, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // a journal is world-readable on purpose
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRun, err)
	}
	return &journal{file: file, out: bufio.NewWriter(file), now: time.Now}, nil
}

func (j *journal) Close() error {
	if j.file == nil {
		return nil
	}
	err := j.out.Flush()
	closeErr := j.file.Close()
	j.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

// pair writes the two rows of one pair.
//
// Both rows are written together and both carry the same pair outcome, so the
// invariant "every pair has exactly two rows" holds by construction and a
// reader never has to join a row to one that has not been written yet. A pair
// nothing could be concluded from is written as `pending` on both rows: it
// happened, it cost wall-clock, and it is not evidence.
func (j *journal) pair(header *Header, split any, result pairResult, reading stats.Reading, override string) {
	outcome := override
	if outcome == "" {
		outcome = strconv.Itoa(result.outcome)
	}
	base := TrialRecord{
		SchemaVersion: SchemaVersion,
		Edit:          header.Edit,
		Split:         fmt.Sprint(split),
		Task:          result.task.ID,
		Repeat:        result.repeat,
		Seed:          result.seed,
		Pool:          header.Pool,
		SplitSHA256:   header.Suite.Digest,
		FleetSHA256:   header.FleetSHA256,
		PreregCommit:  header.PreRegistration.Commit,
		RunID:         header.RunID,
		PairID:        result.task.ID + "#" + strconv.Itoa(result.repeat),
		PairOutcome:   outcome,
		TaskRepeats:   result.taskRepeats,

		CumWins:     reading.Evidence.Wins,
		CumLosses:   reading.Evidence.Losses,
		CumTies:     reading.Evidence.Ties,
		LostTasks:   reading.Evidence.LostTasks,
		LogEGain:    reading.LogEGain,
		LogEHarm:    reading.LogEHarm,
		DeltaLo:     reading.Delta.Lo,
		DeltaHi:     reading.Delta.Hi,
		AllocReason: "floor",
		TS:          j.now().UTC().Format(time.RFC3339),
	}
	for _, side := range []struct {
		arm     Arm
		outcome Outcome
		digest  string
		cached  bool
	}{
		{ArmBase, result.base, header.Baseline.TreeSHA256, result.cached},
		{ArmEdit, result.edit, header.Candidate.TreeSHA256, false},
	} {
		row := base
		row.Arm = side.arm.String()
		row.HarnessSHA256 = side.digest
		row.Pass = side.outcome.Pass
		row.SelectionKind = side.outcome.SelectionKind
		row.ErrorClass = side.outcome.ErrorClass
		row.Error = side.outcome.Error
		row.TraceRunID = side.outcome.RunID
		row.WallMS = side.outcome.WallMS
		row.TokensIn = side.outcome.TokensIn
		row.TokensOut = side.outcome.TokensOut
		row.Candidates = side.outcome.Candidates
		row.CacheHit = side.cached
		j.write(row)
	}
}

func (j *journal) write(row TrialRecord) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.out == nil {
		return
	}
	body, err := json.Marshal(row)
	if err != nil {
		return
	}
	_, _ = j.out.Write(body)
	_ = j.out.WriteByte('\n')
	_ = j.out.Flush()
}

// ReadTrials reads a journal back.
func ReadTrials(name string) ([]TrialRecord, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the journal
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRun, err)
	}
	var rows []TrialRecord
	for line := range splitLines(string(body)) {
		if line == "" {
			continue
		}
		var row TrialRecord
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrRun, name, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// writeHeader writes run.json.
func writeHeader(dir string, header Header) error {
	return writeJSON(filepath.Join(dir, HeaderName), header)
}

// writeAA writes aa.json.
func writeAA(dir string, aa AAResult) error {
	return writeJSON(filepath.Join(dir, AAName), aa)
}

// ReadHeader reads a run.json back.
func ReadHeader(name string) (Header, error) {
	var header Header
	if err := readJSON(name, &header); err != nil {
		return Header{}, err
	}
	if header.SchemaVersion != SchemaVersion {
		return Header{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrRun, name, header.SchemaVersion, SchemaVersion)
	}
	return header, nil
}

func writeJSON(name string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	if err := os.WriteFile(name, append(body, '\n'), 0o644); err != nil { //nolint:gosec // a record is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	return nil
}

// splitLines yields a text's lines without carrying the whole slice.
func splitLines(text string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for len(text) > 0 {
			line := text
			if i := indexByte(text, '\n'); i >= 0 {
				line, text = text[:i], text[i+1:]
			} else {
				text = ""
			}
			if !yield(line) {
				return
			}
		}
	}
}

func indexByte(s string, b byte) int {
	for i := range len(s) {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func joinPath(parts ...string) string { return filepath.Join(parts...) }
func dirOf(p string) string           { return filepath.Dir(filepath.Clean(p)) }

// apply moves the edit's own document, or says why it did not.
//
// Autonomy is the surface's, not the run's: a memory note or a skill is
// accepted by the harness without asking anybody, and every other surface
// needs a person. A regression is the one transition that never needs one — an
// edit that made things worse is rejected by the evidence, and there is
// nothing for a reviewer to weigh.
func apply(o Options, edit doc.Edit, result Result) (Transition, error) {
	autonomy, err := autonomyOf(edit.Component)
	if err != nil {
		return Transition{}, err
	}
	transition := Transition{Autonomy: autonomy, Status: edit.Status.String()}
	regressed := verdictOf(result.Splits, vocab.SplitHeldIn) == vocab.VerdictRegress ||
		verdictOf(result.Splits, vocab.SplitHeldOut) == vocab.VerdictRegress

	switch {
	case o.AA:
		transition.Reason = "an A/A calibration measures the baseline against itself and says nothing about the edit"
		return transition, nil
	case regressed:
		transition.Status = vocab.StatusRejected.String()
		transition.Approval = edit.Approval.String()
		transition.Reason = "a split regressed"
	case !result.Header.Promote:
		transition.Reason = "the run did not establish a gain on either split with no regression on both, so the proposal stands as it was"
		return transition, nil
	case autonomy == surfaces.AutonomyAutoAccept:
		transition.Status = vocab.StatusAccepted.String()
		transition.Approval = vocab.ApprovalAuto.String()
		transition.Reason = fmt.Sprintf("both splits held or improved and %s is accepted without a person", edit.Component)
	case autonomy == surfaces.AutonomyProposeOnly:
		// An edit to a propose-only surface is never accepted, by anyone. The
		// vault reports an accepted one as propose_only_accepted, so telling a
		// reviewer to accept it would be telling them to create a finding.
		//
		// No propose-only surface has an injection point today, so a run
		// refuses such an edit before it gets here and this branch is
		// unreachable. It is written anyway: the two facts — which surfaces
		// are propose-only, and which are file-shaped — come from different
		// places and are not required to stay disjoint.
		transition.Reason = fmt.Sprintf(
			"both splits held or improved, but %s is propose-only: an edit to it is evidence, not a change to make",
			edit.Component)
		transition.Instruction = fmt.Sprintf(
			"%s stands as a measured proposal; a propose-only surface is changed by hand, outside this loop",
			mustPath(edit))
		return transition, nil
	default:
		transition.Reason = fmt.Sprintf(
			"both splits held or improved, but %s is a %s surface", edit.Component, autonomy)
		transition.Instruction = fmt.Sprintf(
			"review %s and, to accept it, set status: accepted, approval: human and approved_by: <name>",
			mustPath(edit))
		return transition, nil
	}

	if err := write(o.Vault, edit, transition); err != nil {
		return Transition{}, err
	}
	transition.Applied = true
	return transition, nil
}

// autonomyOf reads a surface's autonomy word.
func autonomyOf(component string) (string, error) {
	for _, autonomy := range surfaces.Autonomies() {
		names, err := surfaces.ByAutonomy(autonomy)
		if err != nil {
			return "", err
		}
		for _, name := range names {
			if name == component {
				return autonomy, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %q is not an editable harness surface", ErrRun, component)
}

// write moves the edit document's status, editing the two lines that change
// and nothing else.
//
// The obvious implementation — parse, set the fields, re-render — is lossy in
// a way its own comment would not admit: this package's frontmatter struct is
// closed, so a key somebody added by hand is silently deleted, and the body's
// blank lines are normalised. A run is allowed to record what it measured; it
// is not allowed to edit prose it did not write. So the status and approval
// lines are replaced in place, and if either is not where the writer puts it
// the run says so rather than guessing.
//
// The change is legal under the vault's append-only check because the
// committed status is `proposed`, which is not one of the immutable ones.
func write(vault string, edit doc.Edit, transition Transition) error {
	relative, err := edit.Path()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	name := filepath.Join(vault, filepath.FromSlash(relative))
	body, err := os.ReadFile(name) //nolint:gosec // a document this run just read
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	text := string(body)
	replace := func(key, value string) error {
		if value == "" {
			return nil
		}
		line := key + ": " + value
		for _, was := range []string{"\n" + key + ": ", "\n" + key + ":\t"} {
			at := strings.Index(text, was)
			if at < 0 {
				continue
			}
			end := strings.IndexByte(text[at+1:], '\n')
			if end < 0 {
				break
			}
			text = text[:at+1] + line + text[at+1+end:]
			return nil
		}
		return fmt.Errorf("%w: %s writes no `%s:` line to move", ErrRun, relative, key)
	}
	if err := replace("status", transition.Status); err != nil {
		return err
	}
	if err := replace("approval", transition.Approval); err != nil {
		return err
	}
	// The result has to still parse as the edit it was, or the run has just
	// corrupted a document rather than moved it.
	if _, err := doc.ParseEdit(edit.EditID, []byte(text)); err != nil {
		return fmt.Errorf("%w: rewriting %s would not read back: %w", ErrRun, relative, err)
	}
	if err := os.WriteFile(name, []byte(text), 0o644); err != nil { //nolint:gosec // a vault document is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	return nil
}

func mustPath(edit doc.Edit) string {
	relative, err := edit.Path()
	if err != nil {
		return edit.EditID
	}
	return relative
}

// record writes one vault document per split.
//
// A run document is a measurement and never a decision, which is why it has no
// status and why one is written for each split rather than one for the pair:
// the two splits answer two different questions, and a single document would
// have to pick one of them to be about.
func record(o Options, suite Suite, edit doc.Edit, result *Result) ([]string, error) {
	if o.AA {
		return nil, nil
	}
	day := o.Now().UTC().Format(vocab.DayLayout)
	trace, err := filepath.Rel(o.Vault, o.Out)
	if err != nil || len(trace) > 2 && trace[:2] == ".." {
		trace = filepath.Base(o.Out)
	}
	var written []string
	for _, split := range []vocab.Split{vocab.SplitHeldIn, vocab.SplitHeldOut} {
		reading, ran := result.Splits[split]
		if !ran {
			continue
		}
		document := doc.Run{
			Edit: edit.EditID, Day: day, ModelSlug: result.Header.Pool, Split: split,
			Title: fmt.Sprintf("%s on the %s split of %s: %s",
				edit.EditID, split, suite.ID, reading.Verdict),
			Date:    day,
			Verdict: reading.Verdict,
			Suite:   suite.ID,
			Trials:  reading.Pairs,
			Trace:   filepath.ToSlash(trace),
			Body:    body(result.Header, split, reading, result.Rates[split]),
		}
		// The validates edge requires a pass rate and a baseline pass rate. A
		// split that ran no pair has neither, and writing a number for it
		// would put "not measured" into the arithmetic that reads a rate.
		if rates, ok := result.Rates[split]; ok && rates.Pairs > 0 {
			document = document.Measuring(result.Header.Pool, rates.Edited, rates.Baseline)
		}
		name, err := writeDocument(o.Vault, document)
		if err != nil {
			return nil, err
		}
		written = append(written, name)
	}
	result.Header.Documents = written
	return written, nil
}

// body is the prose under a run document's heading: the numbers a reader wants
// without opening the trace, and the sentence that says what they are not.
func body(header Header, split vocab.Split, reading stats.Reading, rates Rates) string {
	params := header.Params[split.String()]
	lines := []string{
		fmt.Sprintf("%d paired trials, %d discordant (d = %.3f).",
			reading.Pairs, reading.Discordant, reading.Discordance),
		fmt.Sprintf("Pass rate: %.3f edited, %.3f baseline, over the same pairs.",
			rates.Edited, rates.Baseline),
		fmt.Sprintf("The edit won %d, the baseline won %d, %d were ties.",
			reading.Evidence.Wins, reading.Evidence.Losses, reading.Evidence.Ties),
		fmt.Sprintf("Anytime-valid interval on the pass-rate difference: [%+.3f, %+.3f] at alpha = %g.",
			reading.Delta.Lo, reading.Delta.Hi, params.Alpha),
		fmt.Sprintf("Tasks the edit lost on every repeat: %d.", reading.Evidence.LostTasks),
		"",
		"The interval is the finding; the verdict is a convenience read off it.",
		"It is a statement about this task set, not about harness edits in general:",
		"the unit of independence for that claim is the task, and there are tens of them.",
		"",
		"Decision rule: " + header.DecisionRule,
	}
	return joinLines(lines)
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

// writeDocument writes one run document, giving it the first sequence number
// the directory does not already hold.
func writeDocument(vault string, document doc.Run) (string, error) {
	for seq := 0; seq <= 999; seq++ {
		document.Seq = seq
		relative, err := document.Path()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRun, err)
		}
		name := filepath.Join(vault, filepath.FromSlash(relative))
		if _, err := os.Stat(name); err == nil {
			continue
		}
		body, err := document.Bytes()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRun, err)
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", fmt.Errorf("%w: %w", ErrRun, err)
		}
		if err := os.WriteFile(name, body, 0o644); err != nil { //nolint:gosec // a vault document is world-readable on purpose
			return "", fmt.Errorf("%w: %w", ErrRun, err)
		}
		return relative, nil
	}
	return "", fmt.Errorf("%w: %s has been measured a thousand times today", ErrRun, document.Edit)
}

// recordUnmeasurable writes one run document per split for a candidate that
// could not be applied. The documents carry no validates edge, because the
// edge requires a pass rate and there is none: a rate nobody measured must not
// enter the arithmetic that reads one.
func recordUnmeasurable(o Options, suite Suite, edit doc.Edit, result *Result) ([]string, error) {
	day := o.Now().UTC().Format(vocab.DayLayout)
	trace, err := filepath.Rel(o.Vault, o.Out)
	if err != nil || strings.HasPrefix(trace, "..") {
		trace = filepath.Base(o.Out)
	}
	var written []string
	for _, split := range []vocab.Split{vocab.SplitHeldIn, vocab.SplitHeldOut} {
		document := doc.Run{
			Edit: edit.EditID, Day: day, ModelSlug: result.Header.Pool, Split: split,
			Title: fmt.Sprintf("%s on the %s split of %s: not measured",
				edit.EditID, split, suite.ID),
			Date:    day,
			Verdict: vocab.VerdictInconclusive,
			Suite:   suite.ID,
			Trace:   filepath.ToSlash(trace),
			Body: joinLines([]string{
				"Nothing was measured: " + result.Header.Unmeasurable.Reason + ".",
				"",
				"The candidate's sidecar diff does not apply to the harness the vault",
				"describes today. An edit accepted since the candidate was written has",
				"moved the text underneath it, so the two arms of a pair could not be",
				"built and no pair was run.",
				"",
				"Baseline tree: " + result.Header.Unmeasurable.Baseline,
				"Baseline edits: " + strings.Join(result.Header.Unmeasurable.Edits, ", "),
				"",
				result.Header.Unmeasurable.Stderr,
				"",
				"This document carries no pass rate, because there is none. A rate",
				"nobody measured must not enter the arithmetic that reads one.",
			}),
		}
		name, err := writeDocument(o.Vault, document)
		if err != nil {
			return nil, err
		}
		written = append(written, name)
	}
	result.Header.Documents = written
	return written, nil
}
