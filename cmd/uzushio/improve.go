package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/mine"
	"github.com/Kaikei-e/uzushio/internal/proposeedit"
	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// improveOptions are the flags of `uzushio improve`.
type improveOptions struct {
	traces           []string
	vault            string
	asOf             string
	minSupport       int
	minProposers     int
	dryRun           bool
	propose          bool
	patterns         []string
	harness          string
	harnessFromVault bool
	cmoa             string
	config           string
	docdag           string
	topic            string
	out              string
}

// newImproveCmd builds `uzushio improve`.
//
// The command is two halves of one loop and they are deliberately separable.
// Mining is deterministic and free: it reads recorded runs and writes down what
// keeps going wrong, and it needs no model, no network and no container. Only
// `--propose` asks the harness to answer a pattern, and that half costs a
// proposal round per pattern. Somebody looking at what the harness is doing
// wrong should not have to pay for a proposal to find out.
func newImproveCmd() *cobra.Command {
	var o improveOptions
	cmd := &cobra.Command{
		Use:   "improve",
		Short: "Mine failure patterns from CMoA traces, and propose harness edits that answer them",
		Long: "improve reads CMoA's recorded runs, writes a failure pattern for every\n" +
			"recurring failure a deterministic rule recognises, and — with --propose —\n" +
			"asks the proposers for one harness edit per pattern, writing each answer\n" +
			"into the vault as a proposed edit.\n\n" +
			"Nothing it writes is accepted. A pattern is a claim about the harness and\n" +
			"an edit is a proposal about what to do; `uzushio run` is what settles\n" +
			"either of them.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runImprove(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVar(&o.traces, "traces", nil,
		"directory of CMoA traces to mine; repeatable")
	flags.StringVar(&o.vault, "vault", ".", "vault root, the directory docdag.yaml sits in")
	flags.StringVar(&o.asOf, "as-of", "", "day the pass is dated and the harness is read for (default today, UTC)")
	flags.IntVar(&o.minSupport, "min-support", mine.DefaultMinRuns,
		"how many distinct runs a failure needs before it becomes a pattern")
	flags.IntVar(&o.minProposers, "min-proposers", mine.DefaultMinProposers,
		"how many distinct proposers a failure needs before it becomes a pattern")
	flags.BoolVar(&o.dryRun, "dry-run", false, "say what would be written and write nothing")
	flags.BoolVar(&o.propose, "propose", false, "ask the proposers for an edit answering each pattern")
	flags.StringArrayVar(&o.patterns, "pattern", nil,
		"propose against this pattern only; repeatable (default: every pattern this pass mined)")
	flags.StringVar(&o.harness, "harness", "", "an already rendered harness directory to propose against")
	flags.BoolVar(&o.harnessFromVault, "harness-from-vault", false,
		"render the harness from the vault's binding edits before proposing")
	flags.StringVar(&o.cmoa, "cmoa", "cmoa", "the cmoa binary")
	flags.StringVar(&o.config, "config", "", "cmoa.json to pass to cmoa propose")
	flags.StringVar(&o.docdag, "docdag", "", "the docdag binary (default: docdag on PATH)")
	flags.StringVar(&o.topic, "topic", proposeedit.DefaultTopic, "the topic a proposed edit is about")
	flags.StringVar(&o.out, "out", "",
		"directory to keep the render, the generated task and the summary under (default: a temporary one)")
	return cmd
}

// improveSummary is what `--out` leaves behind for the layer above: what was
// read, what was written, and what was refused. It is JSON because the caller
// of `improve` in a loop is a program.
type improveSummary struct {
	Day          string                `json:"day"`
	Runs         int                   `json:"runs"`
	Traces       []string              `json:"traces"`
	MinSupport   int                   `json:"min_support"`
	MinProposers int                   `json:"min_proposers"`
	DryRun       bool                  `json:"dry_run"`
	Patterns     []summaryPattern      `json:"patterns"`
	BelowSupport []summaryPattern      `json:"below_support"`
	NotMined     []mine.NotMinedReport `json:"not_mined,omitempty"`
	Proposals    []summaryProposal     `json:"proposals,omitempty"`
	Refusals     []summaryRefusal      `json:"refusals,omitempty"`
	Harness      *render.Manifest      `json:"harness,omitempty"`
}

type summaryPattern struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Action string `json:"action"`
	Path   string `json:"path"`
	// Category and Component are what the document at Path says, which is not
	// always what the rule said: a pattern somebody re-attributed by hand keeps
	// its own reading, and a summary naming the rule's would name a surface the
	// vault does not contain.
	Category  string   `json:"category"`
	Component string   `json:"component"`
	Runs      []string `json:"runs"`
	Added     []string `json:"added,omitempty"`
	Diverged  []string `json:"diverged,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type summaryProposal struct {
	Edit      string `json:"edit"`
	Pattern   string `json:"pattern"`
	Path      string `json:"path"`
	DiffPath  string `json:"diff_path,omitempty"`
	Component string `json:"component"`
	Approval  string `json:"approval"`
	Proposer  string `json:"proposer"`
	// PatternComponent is the surface the pattern blamed. Where it differs from
	// Component the edit answers a failure on one surface by changing another,
	// which may be right and is never silent.
	PatternComponent string `json:"pattern_component,omitempty"`
}

type summaryRefusal struct {
	Pattern  string `json:"pattern"`
	Proposer string `json:"proposer"`
	Reason   string `json:"reason"`
}

// runImprove is the command's body, with the streams injected so the whole of
// it is testable without a subprocess.
func runImprove(ctx context.Context, o improveOptions, out, errOut io.Writer) error {
	if len(o.traces) == 0 {
		return &flagError{err: fmt.Errorf("improve: --traces is required")}
	}
	day := o.asOf
	if day == "" {
		day = time.Now().UTC().Format(vocab.DayLayout)
	}
	if _, err := time.Parse(vocab.DayLayout, day); err != nil {
		return &flagError{err: fmt.Errorf("improve: --as-of %q is not a %s day", o.asOf, vocab.DayLayout)}
	}
	if o.propose && o.harness == "" && !o.harnessFromVault {
		return &flagError{err: fmt.Errorf(
			"improve: --propose needs a harness to propose against: pass --harness <dir> or --harness-from-vault")}
	}
	if o.harness != "" && o.harnessFromVault {
		return &flagError{err: fmt.Errorf("improve: --harness and --harness-from-vault name two harnesses; pass one")}
	}

	runs, err := mine.Load(o.traces...)
	if err != nil {
		return err
	}
	result := mine.Mine(runs, mine.Options{
		MinRuns:      o.minSupport,
		MinProposers: o.minProposers,
	})
	summary := improveSummary{
		Day:          day,
		Runs:         result.Runs,
		Traces:       o.traces,
		MinSupport:   o.minSupport,
		MinProposers: o.minProposers,
		DryRun:       o.dryRun,
		NotMined:     result.NotMined,
	}

	patterns := make([]doc.Pattern, 0, len(result.Buckets))
	for _, bucket := range result.Buckets {
		pattern, err := bucket.Pattern(day)
		if err != nil {
			return err
		}
		patterns = append(patterns, pattern)
	}
	changes, err := mine.Plan(o.vault, patterns)
	if err != nil {
		return err
	}
	if !o.dryRun {
		if err := mine.Apply(o.vault, changes); err != nil {
			return err
		}
	}
	byID := map[string]mine.Bucket{}
	for _, bucket := range result.Buckets {
		id, err := bucket.PatternID()
		if err != nil {
			return err
		}
		byID[id] = bucket
	}
	fmt.Fprintf(out, "read %d runs from %d trace %s\n",
		result.Runs, len(o.traces), plural(len(o.traces), "directory", "directories"))
	for _, change := range changes {
		bucket := byID[change.ID]
		if change.Action == mine.ActionUnreadable {
			fmt.Fprintf(errOut, "%-11s %-44s %-4s %v\n",
				change.Action, change.ID, bucket.Rule.Name, change.Err)
		} else {
			fmt.Fprintf(out, "%-11s %-44s %-4s %s\n",
				change.Action, change.ID, bucket.Rule.Name, supportOf(bucket))
		}
		if len(change.Diverged) > 0 {
			fmt.Fprintf(errOut, "diverged    %-44s the document on disk states its own %s\n",
				change.ID, strings.Join(change.Diverged, " and "))
		}
		entry := summaryPattern{
			ID: change.ID, Rule: bucket.Rule.Name, Action: string(change.Action), Path: change.Path,
			Category: change.Category, Component: change.Component,
			Runs: bucket.Runs, Added: change.Added, Diverged: change.Diverged,
		}
		if change.Err != nil {
			entry.Error = change.Err.Error()
		}
		summary.Patterns = append(summary.Patterns, entry)
	}
	for _, bucket := range result.Below {
		id, err := bucket.PatternID()
		if err != nil {
			return err
		}
		fmt.Fprintf(errOut, "below-support %-40s %-4s %s\n", id, bucket.Rule.Name, supportOf(bucket))
		summary.BelowSupport = append(summary.BelowSupport, summaryPattern{
			ID: id, Rule: bucket.Rule.Name, Action: "below-support",
			Category: bucket.Rule.Category.String(), Component: bucket.Rule.Component, Runs: bucket.Runs,
		})
	}
	for _, report := range result.NotMined {
		fmt.Fprintf(errOut, "not mined   %-44s %-4s %d occurrences: %s\n",
			"-", report.Name, report.Count, report.Reason)
		for _, text := range report.Errors {
			fmt.Fprintf(errOut, "                                                      %s\n", text)
		}
	}
	if len(changes) == 0 {
		fmt.Fprintln(out, "no failure met the support threshold; nothing was written")
	}

	if o.propose {
		if err := proposeFor(ctx, o, day, patterns, &summary, out, errOut); err != nil {
			return err
		}
	}
	if o.out != "" {
		if err := writeSummary(o.out, summary); err != nil {
			return err
		}
	}
	return nil
}

// proposeFor renders the harness and asks for one edit per pattern.
func proposeFor(
	ctx context.Context,
	o improveOptions,
	day string,
	patterns []doc.Pattern,
	summary *improveSummary,
	out, errOut io.Writer,
) error {
	wanted := patterns
	if len(o.patterns) > 0 {
		wanted = nil
		for _, pattern := range patterns {
			if slices.Contains(o.patterns, pattern.PatternID) {
				wanted = append(wanted, pattern)
			}
		}
		for _, id := range o.patterns {
			if !slices.ContainsFunc(wanted, func(p doc.Pattern) bool { return p.PatternID == id }) {
				// A pattern named on the command line that this pass did not
				// mine is read from the vault: proposing against a standing
				// pattern is the ordinary way to answer one somebody wrote by
				// hand, or one an earlier pass left open.
				pattern, found, err := patternFromVault(o.vault, id)
				if err != nil {
					return err
				}
				if !found {
					return &flagError{err: fmt.Errorf(
						"improve: --pattern %s is neither mined by this pass nor in the vault", id)}
				}
				// An edit predicting `expect: fix` against a claim somebody has
				// already resolved or withdrawn is a proposal about a question
				// that is closed, and the vault reports it as one.
				if pattern.Status != vocab.StatusOpen {
					return &flagError{err: fmt.Errorf(
						"improve: --pattern %s is %s; an edit can only be proposed against an open pattern",
						id, pattern.Status)}
				}
				wanted = append(wanted, pattern)
			}
		}
	}
	if len(wanted) == 0 {
		fmt.Fprintln(out, "nothing to propose against")
		return nil
	}

	work := o.out
	if work == "" {
		temp, err := os.MkdirTemp("", "uzushio-improve-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(temp) }()
		work = temp
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}

	harness := o.harness
	if o.harnessFromVault {
		manifest, err := render.Render(ctx, render.Options{
			Vault:  o.vault,
			AsOf:   day,
			Out:    filepath.Join(work, "harness"),
			Force:  true,
			DocDag: o.docdag,
		})
		if err != nil {
			return err
		}
		harness = filepath.Join(work, "harness")
		summary.Harness = &manifest
		fmt.Fprintf(out, "rendered the harness from the vault: %d files, tree %s\n",
			len(manifest.Files), short(manifest.TreeSHA256))
	}

	if !o.dryRun {
		written, err := proposeedit.WriteTopic(o.vault, o.topic, day)
		if err != nil {
			return err
		}
		if written {
			path, _ := proposeedit.TopicPath(o.topic)
			fmt.Fprintf(out, "wrote %s, the subject a proposed harness edit is about\n", path)
		}
	} else if exists, err := proposeedit.TopicExists(o.vault, o.topic); err != nil {
		return err
	} else if !exists {
		path, _ := proposeedit.TopicPath(o.topic)
		fmt.Fprintf(errOut, "would write %s, the subject a proposed harness edit is about\n", path)
	}

	// The numbering is read once and advances across every pattern in this
	// process. Rescanning per pattern would be right only when something was
	// written in between — which `--dry-run` never does, so it would report
	// he-0001 for every one of them.
	numbering, err := proposeedit.NewNumbering(o.vault)
	if err != nil {
		return err
	}
	runner := proposeedit.ExecRunner{Bin: o.cmoa, Config: o.config, Log: errOut}
	for _, pattern := range wanted {
		if err := proposeOne(
			ctx, o, day, harness, work, pattern, runner, numbering, summary, out, errOut); err != nil {
			return err
		}
	}
	return nil
}

// proposeOne runs one proposal round for one pattern.
func proposeOne(
	ctx context.Context,
	o improveOptions,
	day, harness, work string,
	pattern doc.Pattern,
	runner proposeedit.Runner,
	numbering *proposeedit.Numbering,
	summary *improveSummary,
	out, errOut io.Writer,
) error {
	// Five of the fourteen rules attribute to a surface the rendered harness
	// has no place for. Proposing against one of those patterns is allowed —
	// the proposers may still find something worth saying on a surface that
	// does exist — but the resulting edit is then about a different surface
	// from the failure, and that has to be said rather than discovered later.
	if !surfaces.HasInjectionPoint(pattern.Component) {
		fmt.Fprintf(errOut, "no injection point %-27s the pattern blames %s, which the rendered "+
			"harness has no place for; any edit will be about another surface\n",
			pattern.PatternID, pattern.Component)
	}
	dir := filepath.Join(work, "propose", filepath.Base(pattern.PatternID))
	task, err := proposeedit.BuildTask(ctx, harness, dir, pattern)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "proposing against %s (task %s, %d harness files)\n",
		pattern.PatternID, task.ID, len(task.Files))
	runDir, err := runner.Propose(ctx, task.Dir)
	if err != nil {
		return err
	}
	candidates, err := proposeedit.Candidates(runDir)
	if err != nil {
		return err
	}
	proposals, refusals, err := proposeedit.Convert(ctx, candidates, proposeedit.ConvertOptions{
		Harness: harness,
		Pattern: pattern,
		Day:     day,
		Topic:   o.topic,
		Work:    filepath.Join(dir, "convert"),
	})
	if err != nil {
		return err
	}
	for _, refusal := range refusals {
		fmt.Fprintf(errOut, "refused     %-40s %-10s %s\n",
			pattern.PatternID, refusal.Candidate.ProposerID, refusal.Reason)
		summary.Refusals = append(summary.Refusals, summaryRefusal{
			Pattern: pattern.PatternID, Proposer: refusal.Candidate.ProposerID, Reason: refusal.Reason,
		})
	}
	writes, err := proposeedit.Plan(numbering, proposals)
	if err != nil {
		return err
	}
	if !o.dryRun {
		if err := proposeedit.Apply(o.vault, writes); err != nil {
			return err
		}
	}
	for i, write := range writes {
		verb := "proposed"
		if o.dryRun {
			verb = "would propose"
		}
		fmt.Fprintf(out, "%-13s %-9s %-15s %-6s %s\n",
			verb, write.ID, write.Component, write.Approval, strings.Join(proposals[i].Edit.Paths, ", "))
		entry := summaryProposal{
			Edit: write.ID, Pattern: pattern.PatternID, Path: write.Path, DiffPath: write.DiffPath,
			Component: write.Component, Approval: write.Approval,
			Proposer: proposals[i].Candidate.ProposerID,
		}
		if write.Component != pattern.Component {
			entry.PatternComponent = pattern.Component
		}
		summary.Proposals = append(summary.Proposals, entry)
	}
	return nil
}

// patternFromVault reads a pattern the vault already carries.
func patternFromVault(vaultDir, id string) (doc.Pattern, bool, error) {
	relative, err := vocab.Path(vocab.KindPattern, id)
	if err != nil {
		return doc.Pattern{}, false, &flagError{err: err}
	}
	return mine.ReadPattern(filepath.Join(vaultDir, relative))
}

// writeSummary leaves the machine-readable answer beside the work.
func writeSummary(dir string, summary improveSummary) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "improve.json"), append(body, '\n'), 0o644)
}

// supportOf says how much evidence a bucket carries, which is the number that
// decides whether it became a document.
func supportOf(b mine.Bucket) string {
	return fmt.Sprintf("%d %s, %d %s",
		len(b.Runs), plural(len(b.Runs), "run", "runs"),
		len(b.Proposers), plural(len(b.Proposers), "proposer", "proposers"))
}

// plural picks the word for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// short abbreviates a digest for a line a person reads.
func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
