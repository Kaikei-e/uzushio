package proposeedit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
)

// Instruction is what the proposers are asked. It is deliberately three things
// and no more: the failure as it was observed, the shape of the artefact that
// may be changed, and the one change that is allowed.
//
// What it does not do is prescribe the edit. A bundle that names the fix turns
// the proposers into a formatter for a decision already taken, and then the
// measurement that follows is a measurement of nothing. The evidence goes in;
// the diagnosis and the change come back.
func Instruction(pattern doc.Pattern) string {
	var out strings.Builder
	out.WriteString("# Improve the harness so this failure stops happening\n\n")
	out.WriteString("The repository you are looking at is a *harness directory*: the files an\n")
	out.WriteString("agent is given before it ever sees a task. Changing it changes what every\n")
	out.WriteString("future run of the agent is told. It is not the code under test.\n\n")

	out.WriteString("## The failure\n\n")
	fmt.Fprintf(&out, "- pattern: %s\n", pattern.PatternID)
	fmt.Fprintf(&out, "- what goes wrong: %s\n", pattern.Title)
	fmt.Fprintf(&out, "- unsafe control action: %s\n", pattern.Category)
	fmt.Fprintf(&out, "- context it is unsafe in: %s\n", pattern.Context)
	fmt.Fprintf(&out, "- attributed surface: %s\n", pattern.Component)
	fmt.Fprintf(&out, "- seen in %d recorded runs\n", len(pattern.Evidence))
	if body := strings.TrimSpace(pattern.Body); body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}

	out.WriteString("\n## What the harness directory is made of\n\n")
	fmt.Fprintf(&out, "- `%s` — appended to the agent's system message, after a contract you\n",
		surfaces.SystemPromptFile)
	out.WriteString("  cannot see and must not try to replace.\n")
	fmt.Fprintf(&out, "- `%s/<name>.md` — one note per file, all of them shown to the agent in\n",
		surfaces.MemoryDir)
	out.WriteString("  sorted order. This is where standing knowledge belongs.\n")
	fmt.Fprintf(&out, "- `%s/<name>/%s` — a skill. Only its `description:` is shown; the body\n",
		surfaces.SkillsDir, surfaces.SkillFile)
	out.WriteString("  is not. A skill whose description does not carry the point is a skill\n")
	out.WriteString("  that does nothing.\n")

	fmt.Fprintf(&out, "\nA `%s/` or `%s/` directory that is otherwise empty holds a single empty\n",
		surfaces.MemoryDir, surfaces.SkillsDir)
	out.WriteString("`.gitkeep`. It is a placeholder for a surface that exists and is empty; it\n")
	out.WriteString("renders as nothing, and it is not a file to edit, rename or delete.\n")

	out.WriteString("\n## What to do\n\n")
	out.WriteString("1. Say, in one short paragraph headed `Root cause:`, why the failure above\n")
	out.WriteString("   happens. Not what to do about it — why it happens.\n")
	out.WriteString("2. Propose exactly ONE change that would stop it, as a unified diff.\n")
	out.WriteString("3. The change must touch exactly one of the three surfaces above, and no\n")
	out.WriteString("   other path. A diff that touches two of them is refused unread.\n")
	out.WriteString("4. Prefer adding a new file to rewriting an existing one, and prefer the\n")
	fmt.Fprintf(&out, "   `%s/` surface: it is the one an agent reads in full.\n", surfaces.MemoryDir)
	out.WriteString("5. Write for an agent that has never seen this repository and will not be\n")
	out.WriteString("   told which task it is on. A note about one task helps once; a note about\n")
	out.WriteString("   the mistake helps every time.\n")
	fmt.Fprintf(&out, "6. A note has to be a `.md` file directly under `%s/` — not nested, not\n",
		surfaces.MemoryDir)
	fmt.Fprintf(&out, "   any other extension. A skill has to be exactly `%s/<name>/%s`, with\n",
		surfaces.SkillsDir, surfaces.SkillFile)
	out.WriteString("   `<name>` in lowercase letters, digits and hyphens, and a `description:`\n")
	out.WriteString("   in its frontmatter — the description is the only part the agent is shown.\n")
	out.WriteString("7. Do not delete or rename anything, and do not write a literal tab\n")
	out.WriteString("   character. Either one makes the change unrepresentable and it is thrown\n")
	out.WriteString("   away unread.\n")
	return out.String()
}

// Runner asks for candidates. It is an interface because the whole of `improve
// --propose` is worth testing without a model server behind it: the tests drive
// it with a runner that answers from a table of canned diffs, and the exec
// runner is tested on its own against a canned binary.
type Runner interface {
	// Propose runs one proposal round over a task directory and returns the run
	// directory it wrote.
	Propose(ctx context.Context, taskDir string) (string, error)
}

// ExecRunner is the real Runner: `cmoa propose --task <dir> [--config <file>]`,
// which prints the run directory it created on standard output.
type ExecRunner struct {
	// Bin is the cmoa binary. Empty means "cmoa" on PATH.
	Bin string
	// Config is the cmoa.json to pass, or empty for CMoA's own search.
	Config string
	// Log receives cmoa's standard error, line by line, or is nil.
	Log io.Writer
}

// Propose runs cmoa and reads the run directory off its standard output.
func (r ExecRunner) Propose(ctx context.Context, taskDir string) (string, error) {
	bin := r.Bin
	if bin == "" {
		bin = "cmoa"
	}
	args := []string{"propose", "--task", taskDir}
	if r.Config != "" {
		args = append(args, "--config", r.Config)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	if r.Log != nil {
		cmd.Stderr = io.MultiWriter(&errOut, r.Log)
	} else {
		cmd.Stderr = &errOut
	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s propose: %w: %s",
			ErrPropose, bin, err, strings.TrimSpace(errOut.String()))
	}
	dir := strings.TrimSpace(out.String())
	if dir == "" {
		return "", fmt.Errorf("%w: %s propose printed no run directory", ErrPropose, bin)
	}
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("%w: %s propose named %s: %w", ErrPropose, bin, dir, err)
	}
	return dir, nil
}

// Candidate is one proposer's answer, read back out of the run directory.
type Candidate struct {
	// ProposerID is the proposer that answered.
	ProposerID string
	// Model is the model behind it.
	Model string
	// Status is CMoA's word for what came back.
	Status string
	// Diff is the extracted unified diff, empty unless the status is ok.
	Diff []byte
	// Raw is the whole completion, which is where the prose around the diff is.
	Raw string
}

// candidateJSON is candidates/<id>.json, reduced to what this package reads.
type candidateJSON struct {
	ProposerID string `json:"proposer_id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
}

// Candidates reads every candidate a run directory holds, sorted by proposer,
// so the same run always yields the same edits in the same order.
func Candidates(runDir string) ([]Candidate, error) {
	dir := filepath.Join(runDir, "candidates")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPropose, err)
	}
	var out []Candidate
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPropose, err)
		}
		var decoded candidateJSON
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrPropose, name, err)
		}
		id := decoded.ProposerID
		if id == "" {
			id = strings.TrimSuffix(name, ".json")
		}
		candidate := Candidate{ProposerID: id, Model: decoded.Model, Status: decoded.Status}
		candidate.Diff = readOptional(filepath.Join(dir, id+".diff"))
		candidate.Raw = string(readOptional(filepath.Join(dir, id+".raw.txt")))
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProposerID < out[j].ProposerID })
	return out, nil
}

// readOptional reads a file that may not be there, which is the ordinary case
// for the diff of a candidate that produced none.
func readOptional(path string) []byte {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return body
}
