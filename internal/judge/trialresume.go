package judge

// The resume state is deliberately separate from trial.json: preregistration
// overwrites trial.json with the final report, while this file is the immutable
// statement of what a results journal is allowed to resume.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
)

const trialResumeFile = "resume.json"

type trialResumeState struct {
	SchemaVersion int               `json:"schema_version"`
	Fingerprint   string            `json:"fingerprint"`
	Files         map[string]string `json:"files"`
	Binaries      map[string]string `json:"binaries"`
	// Card and Plan expose the canonical identity that Fingerprint binds. They
	// let a later fixed-H assessment prove that its finalist report and journal
	// describe this exact D/R measurement, rather than merely three files with
	// individually valid digests. They are local resume provenance, never a
	// shareable trial report.
	Card TrialCard   `json:"card"`
	Plan []TrialStep `json:"plan"`
}

// resumeFingerprint binds every input that can change a recorded step's
// meaning. The execution time budget is intentionally absent: extending it is
// the normal way to finish an interrupted fixed measurement.
func resumeFingerprint(opts TrialOptions, plan []TrialStep) (trialResumeState, error) {
	state := trialResumeState{SchemaVersion: TrialSchemaVersion, Files: map[string]string{}, Binaries: map[string]string{}}
	add := func(name string) error {
		digest, err := fileDigest(name)
		if err != nil {
			return err
		}
		state.Files[name] = digest
		return nil
	}
	if err := add(opts.Card.Path(opts.Card.Suite)); err != nil {
		return state, err
	}
	for _, manifest := range opts.Manifests {
		if err := add(manifest.Path); err != nil {
			return state, err
		}
	}
	for _, label := range opts.Card.PathsOf(opts.Card.Labels) {
		if err := add(label); err != nil {
			return state, err
		}
	}
	for _, condition := range []string{ConditionBase, ConditionCandidate} {
		if err := add(configForTrial(opts.Card, condition)); err != nil {
			return state, err
		}
		binary, err := trialBinary(opts, condition)
		if err != nil {
			return state, err
		}
		state.Binaries[condition] = binary
	}
	seen := map[string]bool{}
	for _, step := range plan {
		if seen[step.Item] {
			continue
		}
		seen[step.Item] = true
		task, ok := trialTask(opts.Suite, step.Item)
		if !ok {
			return state, fmt.Errorf("%w: the suite holds no item %s", ErrJudge, step.Item)
		}
		dir := opts.Suite.TaskDir(task)
		if err := add(filepath.Join(dir, "task.json")); err != nil {
			return state, err
		}
		// Gold is the expected answer used by the quality report. It is not
		// handed to the judge, but changing it changes what the journal means.
		if err := add(filepath.Join(dir, filepath.FromSlash(task.Gold))); err != nil {
			return state, err
		}
		var spec taskFile
		if err := readJSONFile(filepath.Join(dir, "task.json"), &spec); err != nil {
			return state, err
		}
		for _, name := range []string{spec.Conversation, spec.Rubric, spec.Reference} {
			if name != "" {
				if err := add(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
					return state, err
				}
			}
		}
		for _, candidate := range opts.Suite.Candidates(task) {
			if err := add(candidate); err != nil {
				return state, err
			}
		}
	}
	card := opts.Card
	card.BudgetSeconds = 0
	state.Card = card
	state.Plan = slices.Clone(plan)
	runnerType := "<nil>"
	if opts.Runner != nil {
		runnerType = reflect.TypeOf(opts.Runner).String()
	}
	payload := struct {
		Card     TrialCard   `json:"card"`
		Plan     []TrialStep `json:"plan"`
		Files    [][2]string `json:"files"`
		Binaries [][2]string `json:"binaries"`
		Runner   string      `json:"runner"`
	}{Card: card, Plan: plan, Runner: runnerType}
	for name, digest := range state.Files {
		payload.Files = append(payload.Files, [2]string{name, digest})
	}
	for condition, digest := range state.Binaries {
		payload.Binaries = append(payload.Binaries, [2]string{condition, digest})
	}
	sort.Slice(payload.Files, func(i, j int) bool { return payload.Files[i][0] < payload.Files[j][0] })
	sort.Slice(payload.Binaries, func(i, j int) bool { return payload.Binaries[i][0] < payload.Binaries[j][0] })
	body, err := json.Marshal(payload)
	if err != nil {
		return state, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	sum := sha256.Sum256(body)
	state.Fingerprint = hex.EncodeToString(sum[:])
	return state, nil
}

func configForTrial(card TrialCard, condition string) string {
	if condition == ConditionBase {
		return card.Path(card.Base.Config)
	}
	return card.Path(card.Candidate.Config)
}

func trialTask(suite Suite, id string) (Task, bool) {
	for _, task := range suite.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

// trialBinary returns a content hash for a real runner's selected binary.
// Fake runners do not run a binary, so their stable type is already bound in
// the fingerprint payload and their binary value is deliberately empty.
func trialBinary(opts TrialOptions, condition string) (string, error) {
	// A fake runner has no executable to hash. Its condition binary name still
	// belongs to the card payload above, so changing it cannot resume a run.
	var defaultBinary string
	switch runner := opts.Runner.(type) {
	case CMoATrialRunner:
		defaultBinary = runner.Binary
	case *CMoATrialRunner:
		if runner != nil {
			defaultBinary = runner.Binary
		}
	default:
		return "", nil
	}
	name := opts.Card.Candidate.CMoA
	if condition == ConditionBase {
		name = opts.Card.Base.CMoA
	}
	explicit := name != ""
	if name == "" {
		name = defaultBinary
	}
	if name == "" {
		return "", nil
	}
	if explicit && stringsContainsPathSeparator(name) && !filepath.IsAbs(name) {
		name = opts.Card.Path(name)
	}
	if !filepath.IsAbs(name) && !stringsContainsPathSeparator(name) {
		resolved, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%w: resolve trial binary %q: %w", ErrJudge, name, err)
		}
		name = resolved
	}
	digest, err := ConfigDigest(name)
	if err != nil {
		return "", fmt.Errorf("%w: trial binary %s: %w", ErrJudge, name, err)
	}
	return digest, nil
}

func stringsContainsPathSeparator(name string) bool {
	return filepath.Base(name) != name
}

func ensureTrialResumeState(opts TrialOptions, plan []TrialStep, hasJournal bool) error {
	state, err := resumeFingerprint(opts, plan)
	if err != nil {
		return err
	}
	name := filepath.Join(opts.Out, trialResumeFile)
	body, err := os.ReadFile(name)
	if err == nil {
		var old trialResumeState
		if err := json.Unmarshal(body, &old); err != nil {
			return fmt.Errorf("%w: invalid resume identity %s: %w", ErrJudge, name, err)
		}
		if old.SchemaVersion != TrialSchemaVersion || old.Fingerprint != state.Fingerprint {
			return fmt.Errorf("%w: %s was recorded for different trial inputs; name a new --out", ErrJudge, name)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if hasJournal {
		return fmt.Errorf("%w: %s has completed steps but no resume identity; name a new --out", ErrJudge, filepath.Join(opts.Out, TrialResultsFile))
	}
	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	body, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return ensureTrialResumeState(opts, plan, hasJournal)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return nil
}
