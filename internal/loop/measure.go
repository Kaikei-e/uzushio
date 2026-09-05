package loop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// consecutiveInfraLimit is how many pairs in a row may fail to produce an
// answer before the run gives up.
//
// A pair that could not be measured is not evidence and is not counted, so a
// fleet that has gone away would otherwise burn the whole budget writing
// nothing. Five is enough to ride out a restart and few enough to notice.
const consecutiveInfraLimit = 5

// pair is one scheduled unit of work: one task at one repeat index, run on
// both arms at one seed.
type pair struct {
	index  int
	task   SuiteTask
	repeat int
	seed   int64
	dir    string
	// taskRepeats is how many pairs this split scheduled for this task.
	taskRepeats int
	// base and edit are the rendered harness directories the two arms read.
	// They are carried rather than derived from the arm's name, because the
	// A/A calibration runs the baseline on both and a name-derived directory
	// would make it a mislabelled A/B against the candidate.
	base, edit armSide
}

// armSide is one arm's harness: where it is and what it hashes to.
type armSide struct {
	dir    string
	sha256 string
}

// pairResult is what came back.
type pairResult struct {
	pair
	base    Outcome
	edit    Outcome
	cached  bool
	outcome int
	counted bool
}

// tally is what a split's trials add up to besides the pair statistic: how
// many pairs each task has completed out of the number scheduled for it, how
// many of them the baseline won outright, and how many trials each arm passed.
//
// The pass counts are kept because the pair statistic cannot answer "what did
// this arm score" — a tie is a pair both arms passed or a pair both failed,
// and the two are the same pair outcome and very different pass rates.
type tally struct {
	scheduled map[string]int
	completed map[string]int
	lost      map[string]int
	basePass  int
	editPass  int
	pairs     int
}

func newTally(pairs []pair) *tally {
	t := &tally{
		scheduled: map[string]int{},
		completed: map[string]int{},
		lost:      map[string]int{},
	}
	for _, p := range pairs {
		t.scheduled[p.task.ID]++
	}
	return t
}

// observe folds one counted pair in.
func (t *tally) observe(result pairResult) {
	t.pairs++
	t.completed[result.task.ID]++
	if result.outcome == stats.BaselineWon {
		t.lost[result.task.ID]++
	}
	if result.base.Pass {
		t.basePass++
	}
	if result.edit.Pass {
		t.editPass++
	}
}

// lostTasks counts the tasks the edit lost outright: every one of the task's
// scheduled repeats is in, and the baseline won every one of them.
//
// Both halves matter. Counting a task that is net worse — one loss in three —
// makes the gate fire on an edit that changes nothing about four times in five
// at the discordance the design expects, because after one round-robin pass
// every task has exactly one pair and two unlucky tasks trip it. Counting a
// task whose remaining repeats have not been run yet is the same mistake in
// time rather than in degree: "lost every repeat so far" is not the claim.
func (t *tally) lostTasks() int {
	n := 0
	for id, scheduled := range t.scheduled {
		if scheduled > 0 && t.completed[id] == scheduled && t.lost[id] == scheduled {
			n++
		}
	}
	return n
}

// rate is the share of a split's completed pairs one arm passed. It is a pass
// rate in the ordinary sense — trials passed over trials run — and not a share
// of the pairs the arm won, which reads one half when nothing happened.
func (t *tally) rate(passes int) float64 {
	if t.pairs == 0 {
		return 0
	}
	return float64(passes) / float64(t.pairs)
}

// schedule lays out the pairs of one split.
//
// The order is round-robin over the tasks: every task is run once before any
// task is run twice. That keeps the running statistic from being dominated by
// whichever task happened to be scheduled first, and it means an interrupted
// run is still interpretable — after any prefix, every task has been seen
// within one of every other.
func schedule(suite Suite, split vocab.Split, repeats, cap int, base, edit armSide) ([]pair, error) {
	tasks := suite.Of(split)
	pairs := make([]pair, 0, len(tasks)*repeats)
	for repeat := range repeats {
		for _, task := range tasks {
			if len(pairs) >= cap {
				return pairs, nil
			}
			dir, err := suite.TaskDir(task)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrRun, err)
			}
			pairs = append(pairs, pair{
				index:  len(pairs),
				task:   task,
				repeat: repeat,
				seed:   Seed(task.ID, repeat),
				dir:    dir,
				base:   base,
				edit:   edit,
			})
		}
	}
	// The cap can truncate the last round-robin pass, so a task's scheduled
	// count is not always the mode's K. It is filled in once the schedule is
	// whole and carried on every pair, because it is what the count gate means
	// by "all of them".
	scheduled := map[string]int{}
	for _, p := range pairs {
		scheduled[p.task.ID]++
	}
	for i := range pairs {
		pairs[i].taskRepeats = scheduled[pairs[i].task.ID]
	}
	return pairs, nil
}

// measure runs one split to a decision or to its cap.
func measure(
	ctx context.Context,
	o Options,
	suite Suite,
	split vocab.Split,
	baseline, candidate render.Manifest,
	fleetDigest string,
	journal *journal,
	header *Header,
) (stats.Reading, *tally, error) {
	params := o.Mode.Params(split)
	pairs, err := schedule(suite, split, o.Mode.Repeats(), params.Cap,
		armSide{dir: harnessDir(o, ArmBase), sha256: baseline.TreeSHA256},
		armSide{dir: harnessDir(o, ArmEdit), sha256: candidate.TreeSHA256})
	if err != nil {
		return stats.Reading{}, nil, err
	}
	cache := Cache{Dir: cacheDir(o), Fleet: fleetDigest}

	var evidence stats.Evidence
	counts := newTally(pairs)
	reading := params.Read(evidence)
	infra := 0

	for start := 0; start < len(pairs); start += o.Parallel {
		end := min(start+o.Parallel, len(pairs))
		batch, err := runBatch(ctx, o, suite, pairs[start:end], cache)
		if err != nil {
			return stats.Reading{}, nil, err
		}
		for _, result := range batch {
			if !result.counted {
				infra++
				journal.pair(header, split, result, reading, "pending")
				if infra >= consecutiveInfraLimit {
					// The header is written before the error travels, so a
					// run cut short by an outage leaves a journal that can be
					// read and replayed rather than rows nothing describes.
					header.Aborted = fmt.Sprintf(
						"%d pairs in a row produced no answer on the %s split", infra, split)
					header.FinishedAt = o.Now().UTC().Format(time.RFC3339)
					_ = writeHeader(o.Out, *header)
					return reading, counts, fmt.Errorf(
						"%w: %s; the last said %q",
						ErrRun, header.Aborted, result.base.Error+result.edit.Error)
				}
				continue
			}
			infra = 0
			evidence.Observe(result.outcome)
			counts.observe(result)
			evidence.LostTasks = counts.lostTasks()
			reading = params.Read(evidence)
			journal.pair(header, split, result, reading, "")
		}
		if reading.Decided {
			break
		}
	}
	return reading, counts, nil
}

// runBatch runs a batch of pairs, both arms of each, and returns the results
// in the order they were scheduled — the concurrency is for wall-clock, and
// the record must not depend on which goroutine finished first.
func runBatch(
	ctx context.Context,
	o Options,
	suite Suite,
	batch []pair,
	cache Cache,
) ([]pairResult, error) {
	results := make([]pairResult, len(batch))
	errs := make([]error, len(batch))
	var wait sync.WaitGroup
	for i, p := range batch {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[i], errs[i] = runPair(ctx, o, suite, p, cache)
		}()
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// runPair runs both arms of one pair.
//
// The baseline arm is asked of the cache first. That is the biggest saving in
// the design and it is not an approximation: the baseline harness does not
// change while a night's candidates are measured, so one draw per (task, seed,
// baseline digest) is the same draw every candidate would have made.
func runPair(ctx context.Context, o Options, suite Suite, p pair, cache Cache) (pairResult, error) {
	result := pairResult{pair: p}
	baseTrial := Trial{
		Task: p.task.ID, TaskDir: p.dir, Repeat: p.repeat, Seed: p.seed,
		Arm: ArmBase, Harness: p.base.dir, HarnessSHA256: p.base.sha256,
		SuiteDir: suite.Dir,
	}
	editTrial := baseTrial
	editTrial.Arm = ArmEdit
	editTrial.Harness = p.edit.dir
	editTrial.HarnessSHA256 = p.edit.sha256

	if !o.NoCache {
		if hit, ok := cache.Get(baseTrial); ok {
			result.base, result.cached = hit, true
		}
	}
	if !result.cached {
		out, err := o.Runner.Run(ctx, baseTrial)
		if err != nil {
			return pairResult{}, err
		}
		result.base = sanitise(out, suite.Dir)
		if !o.NoCache {
			cache.Put(baseTrial, result.base)
		}
	}
	out, err := o.Runner.Run(ctx, editTrial)
	if err != nil {
		return pairResult{}, err
	}
	result.edit = sanitise(out, suite.Dir)

	if !result.base.Answered() || !result.edit.Answered() {
		return result, nil
	}
	result.counted = true
	switch {
	case result.edit.Pass && !result.base.Pass:
		result.outcome = stats.EditWon
	case result.base.Pass && !result.edit.Pass:
		result.outcome = stats.BaselineWon
	default:
		result.outcome = stats.Tie
	}
	return result, nil
}

// sanitise takes the machine out of an outcome before anything is written
// down.
//
// A Runner is an interface, so what comes back from one is not this package's
// to trust: the exec runner already keeps its paths relative, and a caller's
// own runner may not. Both channels that carry a home directory into a
// committed record — the trace directory and the harness's own stderr — are
// closed here, once, rather than at each of the three places that write.
func sanitise(outcome Outcome, suiteDir string) Outcome {
	outcome.RunDir = relativeTo(suiteDir, outcome.RunDir)
	outcome.Error = Scrub(outcome.Error)
	return outcome
}

// harnessDir is where one arm's rendered harness was written.
func harnessDir(o Options, arm Arm) string {
	name := "base"
	if arm == ArmEdit {
		name = "edit"
	}
	return joinPath(o.Out, "harness", name)
}

// cacheDir is where baseline outcomes are remembered: beside the run
// directory rather than inside it, so sibling runs of different candidates
// share one baseline draw.
func cacheDir(o Options) string {
	if o.NoCache {
		return ""
	}
	return joinPath(dirOf(o.Out), "cache")
}

// calibrate runs the baseline against itself and reports the discordance.
//
// It is three things at once: the parameter every sample-size calculation
// needs, a self-test that the harness reproduces itself at all, and the floor
// on any effect that can ever be detected. The cache is off for it, because a
// cache would answer both arms from one draw and report a discordance of zero
// it never measured.
func calibrate(
	ctx context.Context,
	o Options,
	suite Suite,
	baseline render.Manifest,
	journal *journal,
	header *Header,
) (*AAResult, error) {
	aa := &AAResult{Harness: baseline.TreeSHA256, StartedAt: o.Now().UTC().Format(time.RFC3339)}
	calibration := o
	calibration.NoCache = true

	// The calibration draws from both splits, in the same round-robin order,
	// so it measures the suite rather than half of it. Both arms are the
	// baseline directory and the baseline digest: the question is whether the
	// harness reproduces itself at one seed, and an arm pointed at the
	// candidate would answer a different one.
	side := armSide{dir: harnessDir(o, ArmBase), sha256: baseline.TreeSHA256}
	perSplit := make([][]pair, 0, len(vocab.AllSplits()))
	for _, split := range vocab.AllSplits() {
		some, err := schedule(suite, split, o.Mode.Repeats(), AAPairs, side, side)
		if err != nil {
			return nil, err
		}
		perSplit = append(perSplit, some)
	}
	// The two splits are interleaved rather than concatenated and cut. A
	// concatenation truncated to thirty is thirty pairs of held-in whenever
	// held-in has ten tasks or more, and the calibration is supposed to
	// measure the suite rather than half of it.
	var pairs []pair
	for i := 0; len(pairs) < AAPairs; i++ {
		took := false
		for _, some := range perSplit {
			if i < len(some) && len(pairs) < AAPairs {
				pairs = append(pairs, some[i])
				took = true
			}
		}
		if !took {
			break
		}
	}
	for i := range pairs {
		pairs[i].index = i
	}

	var evidence stats.Evidence
	params := o.Mode.Params(vocab.SplitHeldIn)
	for start := 0; start < len(pairs); start += o.Parallel {
		end := min(start+o.Parallel, len(pairs))
		batch, err := runBatch(ctx, calibration, suite, pairs[start:end], Cache{})
		if err != nil {
			return nil, err
		}
		for _, result := range batch {
			if !result.counted {
				journal.pair(header, "aa", result, params.Read(evidence), "pending")
				continue
			}
			evidence.Observe(result.outcome)
			journal.pair(header, "aa", result, params.Read(evidence), "")
		}
	}
	aa.Pairs = evidence.Pairs()
	aa.Wins, aa.Losses, aa.Ties = evidence.Wins, evidence.Losses, evidence.Ties
	if aa.Pairs > 0 {
		aa.Discordance = float64(evidence.Discordant()) / float64(aa.Pairs)
	}
	if aa.Discordance > AAWarnAbove {
		aa.Warning = fmt.Sprintf(
			"the baseline disagreed with itself on %.0f%% of pairs; above %.0f%% the fleet's own nondeterminism is the dominant effect and no verdict other than inconclusive is meaningful",
			aa.Discordance*100, AAWarnAbove*100)
	}
	aa.FinishedAt = o.Now().UTC().Format(time.RFC3339)
	o.logf("A/A: %d pairs, discordance %.3f%s", aa.Pairs, aa.Discordance, aa.Warning)
	return aa, nil
}
