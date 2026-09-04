package mutate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/worktree"
)

// ErrMutate is the sentinel every failure to generate wraps.
var ErrMutate = errors.New("mutate")

// MutantDir is where a task keeps its mutants, relative to the task
// directory. It is CMoA's layout, and the manifest names each diff inside it.
const MutantDir = "mutants"

// DefaultMax is how many new mutants one run writes when nobody says. A
// verifier is a container, so every mutant is a container run: fifty is
// already a long check.
const DefaultMax = 50

// Options is one generation run.
type Options struct {
	// Operators are the operators to apply. Empty is all of them.
	Operators []Operator
	// Max caps how many new mutants are produced. Zero is DefaultMax.
	Max int
	// KeepNonViable keeps mutants that do not compile. They are dropped by
	// default: a mutant the toolchain refuses makes `go test ./...` exit
	// non-zero whatever the tests say, so the verifier reports fail and the
	// health check scores it killed — a verifier that did nothing but build
	// would earn those kills for free, and the headline kill rate would be an
	// upper bound rather than a measurement.
	KeepNonViable bool
}

// Plan is what one generation run found: the mutants to write, the mutants
// dropped because they do not compile, and the files nobody looked at.
type Plan struct {
	// Mutants are the diffs to write, in order.
	Mutants []Planned
	// NotViable counts the candidates dropped per operator because the
	// toolchain refused them.
	NotViable map[Operator]int
	// Skipped names the files that were not mutated, and why.
	Skipped []Skipped
}

// Skipped is one file the generator did not look at.
type Skipped struct {
	File   string
	Reason string
}

// NotViableTotal is how many candidates were dropped for not compiling.
func (p *Plan) NotViableTotal() int {
	total := 0
	for _, n := range p.NotViable {
		total += n
	}
	return total
}

// Planned is one mutant that would be written: the file it goes in, the diff
// itself, and the manifest entry that describes it.
type Planned struct {
	// Path is the diff's path relative to the task directory, forward slashed,
	// which is what the manifest carries.
	Path string
	// Diff is the unified diff against the reference-applied tree.
	Diff string
	// Operator is what made it.
	Operator Operator
	// Note describes the change in one line.
	Note string
	// File, Line and Column say where it happened, for a listing.
	File   string
	Line   int
	Column int
}

// Generate produces the mutants a task does not already have.
//
// The tree it mutates is the reference-applied one: a mutant is a defect
// introduced into a working solution, so the diff it carries is a diff against
// the reference rather than against the seed state, and `uzushio task doctor`
// puts the two back together before verifying. The diff is git's own — the
// worktree's index holds the reference-applied state, so the difference
// between the index and a file written over is exactly the mutant, with git's
// a/ and b/ prefixes already naming the repository-relative path. Nothing
// rewrites the diff afterwards, which is why git applies it again without
// complaint.
//
// Every diff is compared by digest against the ones the task already carries —
// both the ones the manifest declares and every diff sitting in mutants/, so an
// orphan left by an interrupted run is not regenerated under a second number.
//
// A candidate that does not compile is dropped. That is a measurement decision
// rather than tidiness: a mutant the toolchain refuses fails `go test ./...`
// whatever the tests do, so the verifier says fail and the health check scores
// it killed — and the operators produce a steady supply of them (a removed
// `:=`, a bumped array length, a subtraction on strings). Filtering here, once,
// keeps the kill rate a statement about the tests rather than about the
// compiler. --keep-nonviable turns it off.
func Generate(ctx context.Context, t *task.Task, opts Options) (plan *Plan, err error) {
	if t.Reference == nil {
		return nil, fmt.Errorf("%w: task mutate needs task.json version 2 with a reference", ErrMutate)
	}
	operators := opts.Operators
	if len(operators) == 0 {
		operators = AllOperators()
	}
	maximum := opts.Max
	if maximum == 0 {
		maximum = DefaultMax
	}
	if maximum < 0 {
		return nil, fmt.Errorf("%w: --max %d is negative", ErrMutate, maximum)
	}

	seen, err := digests(t)
	if err != nil {
		return nil, err
	}
	next, err := nextIndex(t)
	if err != nil {
		return nil, err
	}
	plan = &Plan{NotViable: map[Operator]int{}}

	tree, err := worktree.Add(ctx, t.Repo, t.Rev)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMutate, err)
	}
	// A worktree that will not go away leaves a registration behind in the
	// task's repository, which the next run trips over.
	defer func() {
		if closeErr := tree.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("%w: %w", ErrMutate, closeErr)
		}
	}()
	if err := tree.Apply(ctx, t.AbsPath(t.Reference.Path)); err != nil {
		return nil, fmt.Errorf("%w: apply the reference: %w", ErrMutate, err)
	}

	for _, file := range t.Files {
		src, err := tree.Read(file)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMutate, err)
		}
		if reason, skip := Skip(file, src); skip {
			plan.Skipped = append(plan.Skipped, Skipped{File: file, Reason: reason})
			continue
		}
		mutations, err := Mutations(file, src, operators)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMutate, err)
		}
		for _, mutation := range mutations {
			if len(plan.Mutants) >= maximum {
				return plan, nil
			}
			diff, viable, err := candidate(ctx, tree, file, mutation.Source, !opts.KeepNonViable)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrMutate, err)
			}
			if !viable {
				plan.NotViable[mutation.Operator]++
				continue
			}
			if strings.TrimSpace(diff) == "" {
				continue
			}
			digest := digestOf(diff)
			if seen[digest] {
				continue
			}
			seen[digest] = true
			plan.Mutants = append(plan.Mutants, Planned{
				Path:     fmt.Sprintf("%s/%04d-%s-%s-L%dC%d.diff", MutantDir, next, mutation.Operator, stem(file), mutation.Line, mutation.Column),
				Diff:     diff,
				Operator: mutation.Operator,
				Note:     mutation.Note,
				File:     file,
				Line:     mutation.Line,
				Column:   mutation.Column,
			})
			next++
		}
	}
	return plan, nil
}

// candidate writes one mutated file into the worktree, asks the toolchain
// whether it is still a program, reads the difference back out, and puts the
// file back. The write and the restore are a pair: a mutant is one file changed
// at a time, and the tree the next one is cut from has to be the
// reference-applied tree again.
func candidate(
	ctx context.Context, tree *worktree.Tree, file string, source []byte, check bool,
) (diff string, viable bool, err error) {
	if err := tree.Write(file, source); err != nil {
		return "", false, err
	}
	defer func() {
		if restoreErr := tree.Restore(ctx, file); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	if check {
		ok, err := builds(ctx, tree.Dir())
		if err != nil {
			return "", false, err
		}
		if !ok {
			return "", false, nil
		}
	}
	diff, err = tree.Unstaged(ctx, file)
	if err != nil {
		return "", false, err
	}
	return diff, true, nil
}

// builds reports whether the worktree still compiles.
//
// It is the host toolchain rather than the task's container: the check is about
// the Go source alone, it runs once per candidate at generation time and never
// during a health check, and a compile against a warm build cache is cheap
// where a container is not. GOWORK is off and GOTOOLCHAIN is local so that a
// workspace file or a toolchain directive somewhere above the temporary
// worktree cannot change the answer from one machine to the next.
func builds(ctx context.Context, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off", "GOTOOLCHAIN=local")
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A non-zero exit is the compiler's answer, which is the answer being
		// asked for. Anything else — no toolchain, a cancelled context — is a
		// failure to ask the question at all.
		return false, nil
	}
	return false, fmt.Errorf("go build in the worktree: %w: %s", err, strings.TrimSpace(errOut.String()))
}

// Write materialises the planned mutants under the task directory and returns
// the manifest entries that describe them.
func Write(t *task.Task, planned []Planned) ([]task.MutantManifest, error) {
	entries := make([]task.MutantManifest, 0, len(planned))
	for _, mutant := range planned {
		target := t.AbsPath(mutant.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("%w: create %s: %w", ErrMutate, filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(mutant.Diff), 0o644); err != nil { //nolint:gosec // a diff is world-readable on purpose
			return nil, fmt.Errorf("%w: write %s: %w", ErrMutate, target, err)
		}
		entries = append(entries, task.MutantManifest{
			Diff:     mutant.Path,
			Expect:   task.ExpectKilled.String(),
			Origin:   task.OriginGenerated.String(),
			Operator: mutant.Operator.String(),
			Note:     mutant.Note,
		})
	}
	return entries, nil
}

// digests returns the digest of every mutant diff the task already carries, so
// a mutation that produces one of them again is skipped rather than written
// under a second name.
//
// It reads the manifest and the directory, which is the same union nextIndex
// takes, and for the same reason: a diff on disk that nobody declared is still
// a mutant that exists. An interrupted run leaves exactly that behind, and a
// dedupe that saw only the manifest would write every one of them again.
func digests(t *task.Task) (map[string]bool, error) {
	out := map[string]bool{}
	add := func(path string) error {
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// A manifest entry whose diff is missing is the task's
				// problem, and `task doctor` reports it. Generating is not the
				// moment to refuse the whole run over it.
				return nil
			}
			return fmt.Errorf("%w: %w", ErrMutate, err)
		}
		out[digestOf(string(body))] = true
		return nil
	}
	for _, mutant := range t.Mutants {
		if err := add(t.AbsPath(mutant.Diff)); err != nil {
			return nil, err
		}
	}
	dir := t.AbsPath(MutantDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %w", ErrMutate, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".diff" {
			continue
		}
		if err := add(filepath.Join(dir, entry.Name())); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// numbered is the index a mutant's file name opens with.
var numbered = regexp.MustCompile(`^(\d{4})-`)

// nextIndex is one past the highest number the task already uses. It reads
// both the manifest and the directory, because a diff on disk that nobody
// declared is still a name taken.
func nextIndex(t *task.Task) (int, error) {
	highest := 0
	consider := func(name string) {
		match := numbered.FindStringSubmatch(name)
		if match == nil {
			return
		}
		if n, err := strconv.Atoi(match[1]); err == nil && n > highest {
			highest = n
		}
	}
	for _, mutant := range t.Mutants {
		consider(path.Base(mutant.Diff))
	}
	entries, err := os.ReadDir(t.AbsPath(MutantDir))
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("%w: %w", ErrMutate, err)
	}
	for _, entry := range entries {
		consider(entry.Name())
	}
	return highest + 1, nil
}

// digestOf is the identity of a diff's bytes.
func digestOf(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])
}

// stem is a file's name without its directory or its extension, which is the
// readable part of a mutant's file name.
func stem(file string) string {
	base := path.Base(file)
	return strings.TrimSuffix(base, path.Ext(base))
}

// Operators parses a comma-separated operator list, refusing an unknown name
// rather than silently generating a smaller set than the caller asked for.
func Operators(list string) ([]Operator, error) {
	if strings.TrimSpace(list) == "" {
		return AllOperators(), nil
	}
	var out []Operator
	for _, name := range strings.Split(list, ",") {
		op, err := ParseOperator(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, op) {
			out = append(out, op)
		}
	}
	return out, nil
}
