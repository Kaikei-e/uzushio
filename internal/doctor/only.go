package doctor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// The two selectors that name a whole half of the check.
const (
	// OnlyReference selects every reference run.
	OnlyReference = "reference"
	// OnlyMutants selects every mutant.
	OnlyMutants = "mutants"
)

// Only is a set of selectors saying which verifications to run.
//
// A full check of the banded task is ninety minutes, and almost none of it
// answers the question in front of the person running it. Adding one mutant
// asks about one mutant; a drift check asks whether the reference still passes;
// only a re-calibration asks about everything. The empty set is everything,
// so a check with no selectors is the check that existed before this did.
//
// A selector that matches nothing is refused rather than ignored. The failure
// it prevents is a typo that produces a green report about a mutant nobody
// verified — which is exactly the shape of wrong answer this whole package
// exists to make impossible.
type Only []string

// Empty reports whether every verification is selected.
func (o Only) Empty() bool { return len(o) == 0 }

// Wants reports whether a selector names something.
func (o Only) Wants(selector string) bool {
	for _, s := range o {
		if s == selector {
			return true
		}
	}
	return false
}

// matchesReference reports whether a reference run labelled label is selected.
func (o Only) matchesReference(label string) bool {
	return o.Empty() || o.Wants(OnlyReference) || o.Wants(label)
}

// matchesMutant reports whether a mutant is selected, by the group, by its
// label, or by the name of its diff.
//
// The diff is matched three ways because there are three things a person has in
// front of them: the path as `task.json` writes it, the file name, and the file
// name without `.diff` — which is what the report's own labels are built from
// and what a person reads off `mutants/`.
func (o Only) matchesMutant(label, diff string) bool {
	if o.Empty() || o.Wants(OnlyMutants) || o.Wants(label) {
		return true
	}
	base := filepath.Base(filepath.FromSlash(diff))
	return o.Wants(diff) || o.Wants(base) || o.Wants(strings.TrimSuffix(base, filepath.Ext(base)))
}

// Unmatched returns the selectors that named nothing among the verifications
// that were planned, sorted.
func (o Only) Unmatched(matched map[string]bool) []string {
	var out []string
	for _, s := range o {
		if !matched[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// ErrNoSuchSelector is a --only that named nothing. It is its own sentinel
// because the caller answers it with a usage exit code: nothing was measured
// and nothing is wrong with the task.
func errNoSuchSelector(unmatched, available []string) error {
	return fmt.Errorf(
		"%w: --only %s matched nothing. This task offers %s, %s, and: %s",
		ErrDoctor, strings.Join(unmatched, ", "), OnlyReference, OnlyMutants,
		strings.Join(available, ", "))
}
