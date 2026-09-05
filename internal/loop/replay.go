package loop

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Replay recomputes a run's verdicts from its journal and compares them with
// what the run stored.
//
// It is the acceptance criterion for the whole record, not a convenience: the
// claim the design makes is that the per-trial journal is sufficient to
// re-derive every decision offline, and this is the only thing that can
// falsify it. If the journal and the header ever disagree, the header is a
// summary of something that is no longer reachable.
func Replay(headerName string) (Comparison, error) {
	header, err := ReadHeader(headerName)
	if err != nil {
		return Comparison{}, err
	}
	rows, err := ReadTrials(filepath.Join(filepath.Dir(headerName), TrialsName))
	if err != nil {
		return Comparison{}, err
	}
	comparison := Comparison{Header: header, Recomputed: map[string]stats.Reading{}}

	// The pair outcome is recomputed from the two arm rows rather than read
	// back from the row that stored it. Storing it and reading it back would
	// put the pairing arithmetic — which is half the design — outside the
	// invariant this function exists to check; a journal whose `pass` fields
	// had all been flipped would replay clean.
	type joined struct {
		order      int
		split      string
		task       string
		base, edit *TrialRecord
		stored     string
	}
	pairs := map[string]*joined{}
	var order []*joined
	for i := range rows {
		row := &rows[i]
		key := row.Split + "\x00" + row.PairID
		entry, ok := pairs[key]
		if !ok {
			entry = &joined{order: len(order), split: row.Split, task: row.Task}
			pairs[key] = entry
			order = append(order, entry)
		}
		switch Arm(row.Arm) {
		case ArmBase:
			entry.base = row
		case ArmEdit:
			entry.edit = row
			entry.stored = row.PairOutcome
		default:
			comparison.Anomalies = append(comparison.Anomalies,
				fmt.Sprintf("pair %s of split %s has a row on arm %q", row.PairID, row.Split, row.Arm))
		}
	}

	type splitState struct {
		evidence  stats.Evidence
		scheduled map[string]int
		completed map[string]int
		lost      map[string]int
		params    stats.Params
	}
	states := map[string]*splitState{}
	stateFor := func(split string) *splitState {
		state, ok := states[split]
		if !ok {
			state = &splitState{
				scheduled: map[string]int{}, completed: map[string]int{}, lost: map[string]int{},
				params: header.Params[split],
			}
			states[split] = state
		}
		return state
	}

	for _, entry := range order {
		// Every pair has exactly two rows. It is asserted rather than
		// assumed: a journal with an odd row is one an interrupted process
		// wrote, and the arithmetic over it is not the arithmetic the run did.
		if entry.base == nil || entry.edit == nil {
			comparison.Anomalies = append(comparison.Anomalies, fmt.Sprintf(
				"pair %s of split %s does not have one row on each arm",
				pairIDOf(entry.base, entry.edit), entry.split))
			continue
		}
		state := stateFor(entry.split)
		state.scheduled[entry.task] = entry.edit.TaskRepeats

		outcome, counted := pairOutcome(*entry.base, *entry.edit)
		want := "pending"
		if counted {
			want = strconv.Itoa(outcome)
		}
		if entry.stored != want {
			comparison.Anomalies = append(comparison.Anomalies, fmt.Sprintf(
				"pair %s of split %s: the two arms say %s, the journal recorded %q",
				entry.edit.PairID, entry.split, want, entry.stored))
		}
		if !counted {
			continue
		}
		state.evidence.Observe(outcome)
		state.completed[entry.task]++
		if outcome == stats.BaselineWon {
			state.lost[entry.task]++
		}
		state.evidence.LostTasks = lostFrom(state.scheduled, state.completed, state.lost)
	}

	for split, state := range states {
		comparison.Recomputed[split] = state.params.Read(state.evidence)
	}
	for _, stored := range header.Verdicts {
		got, ran := comparison.Recomputed[stored.Split]
		if !ran {
			comparison.Anomalies = append(comparison.Anomalies,
				fmt.Sprintf("split %s has a stored verdict and no trials", stored.Split))
			continue
		}
		if got.Verdict != stored.Reading.Verdict {
			comparison.Anomalies = append(comparison.Anomalies, fmt.Sprintf(
				"split %s: the journal says %s, run.json says %s",
				stored.Split, got.Verdict, stored.Reading.Verdict))
		}
		if got.Evidence != stored.Reading.Evidence {
			comparison.Anomalies = append(comparison.Anomalies, fmt.Sprintf(
				"split %s: the journal tallies %+v, run.json says %+v",
				stored.Split, got.Evidence, stored.Reading.Evidence))
		}
	}
	comparison.Promote = stats.Promote(
		replayVerdict(comparison.Recomputed, vocab.SplitHeldIn),
		replayVerdict(comparison.Recomputed, vocab.SplitHeldOut))
	if comparison.Promote != header.Promote {
		comparison.Anomalies = append(comparison.Anomalies, fmt.Sprintf(
			"the journal would %spromote and run.json says %spromote",
			not(comparison.Promote), not(header.Promote)))
	}
	return comparison, nil
}

// pairOutcome is the pairing arithmetic, recomputed from the two arm rows: the
// same rule the run applied, spelled once here and once in runPair, so the two
// can be compared. A pair either arm did not answer is not counted.
func pairOutcome(base, edit TrialRecord) (outcome int, counted bool) {
	if base.ErrorClass != ErrorNone || edit.ErrorClass != ErrorNone {
		return 0, false
	}
	switch {
	case edit.Pass && !base.Pass:
		return stats.EditWon, true
	case base.Pass && !edit.Pass:
		return stats.BaselineWon, true
	}
	return stats.Tie, true
}

// lostFrom is the count gate's statistic, recomputed: the tasks whose
// scheduled repeats are all in and every one of which the baseline won. How
// many were scheduled is read off the rows rather than counted from them — a
// split that stopped early holds fewer rows than it scheduled, and counting
// rows would make the replay say a half-run task was lost outright.
func lostFrom(scheduled, completed, lost map[string]int) int {
	n := 0
	for id, count := range scheduled {
		if count > 0 && completed[id] == count && lost[id] == count {
			n++
		}
	}
	return n
}

// pairIDOf names a half-written pair by whichever row is present.
func pairIDOf(base, edit *TrialRecord) string {
	if edit != nil {
		return edit.PairID
	}
	if base != nil {
		return base.PairID
	}
	return "(unnamed)"
}

// Comparison is what a replay found.
type Comparison struct {
	Header     Header
	Recomputed map[string]stats.Reading
	Promote    bool
	// Anomalies is empty where the journal and the header agree, which is the
	// invariant. Anything in it is a record that has come apart.
	Anomalies []string
}

// Identical reports the invariant.
func (c Comparison) Identical() bool { return len(c.Anomalies) == 0 }

func replayVerdict(readings map[string]stats.Reading, split vocab.Split) vocab.Verdict {
	if reading, ok := readings[split.String()]; ok {
		return reading.Verdict
	}
	return vocab.VerdictInconclusive
}

func not(v bool) string {
	if v {
		return ""
	}
	return "not "
}
