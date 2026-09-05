// Package pairwise turns a corpus of pairwise human judgments into three-way
// selection items.
//
// The input is what every public preference corpus has: a set of prompts, two
// systems' answers to one of them, and a person's verdict on which answer is
// better. The output is what a judge that picks one of three answers has to be
// measured on: a prompt, three answers, and one label saying which of the three
// a person would have chosen.
//
// The derivation is the interesting part, and it is deliberately conservative.
//
//   - A triple is kept only where all three of its pairs carry a human verdict.
//     A missing pair is not filled in from the other two: transitivity is the
//     assumption under test, not a licence to invent data.
//   - A triple whose three majorities cycle — x over y, y over z, z over x — is
//     discarded rather than resolved. There is no true answer for such an item,
//     and labelling one anyway pushes noise straight into the validity
//     coefficient the whole exercise exists to estimate. The discard rate is
//     itself a measurement: it is a property of the human labels, and it bounds
//     the agreement any judge could reach.
//   - A Bradley-Terry fit runs beside the majority rule rather than instead of
//     it. BT assumes transitivity and so silently paints over the cycles the
//     acyclic check is looking for; running both and recording where they
//     disagree is what marks an item as hard.
//   - Items are stratified by the margin between the best and the second-best
//     system, because kappa is sensitive to how obvious the answer is. A corpus
//     of easy items reports a coefficient that says nothing about the hard ones,
//     and a corpus of only hard items reports a coefficient near zero that says
//     nothing at all.
//
// Nothing here knows which corpus it is reading. The adapter that names a
// dataset, its fields and its licence lives with the command that fetches it.
package pairwise

import (
	"errors"
	"fmt"
	"slices"
)

// ErrCorpus is the sentinel every malformed-input failure wraps.
var ErrCorpus = errors.New("pairwise: invalid corpus")

// Message is one turn of a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// The verdicts a human comparison can carry. A corpus that spells them
// differently is the adapter's problem, not this package's.
const (
	// WinnerFirst says the first system's answer was preferred.
	WinnerFirst = "first"
	// WinnerSecond says the second's was.
	WinnerSecond = "second"
	// WinnerTie says neither was, whether because they were judged equal or
	// because both were judged bad. The distinction is not one a majority can
	// use, and a corpus that draws it folds both into this.
	WinnerTie = "tie"
)

// Vote is one person's comparison of two systems on one prompt.
type Vote struct {
	First   string
	Second  string
	Winner  string
	Labeler string
}

// Group is one prompt, the answers the systems gave to it, and the human
// comparisons between them.
type Group struct {
	// ID names the prompt. Two answers are comparable only inside one group.
	ID string
	// Conversation is the turns every candidate shares, ending with the user
	// turn the answers respond to.
	Conversation []Message
	// Answers holds each system's answer verbatim. They are somebody else's
	// text and are never rewritten.
	Answers map[string]string
	// Votes are the human comparisons.
	Votes []Vote
	// systems is the insertion order of Answers, so a derivation over a map is
	// still deterministic.
	systems []string
}

// Corpus is a set of groups, in the order they were first seen.
type Corpus struct {
	groups map[string]*Group
	order  []string
}

// NewCorpus returns an empty corpus.
func NewCorpus() *Corpus { return &Corpus{groups: map[string]*Group{}} }

// Add records one comparison, with the two answers it was made over and the
// turns the two systems shared. The conversation of a group is written by
// whichever comparison arrives first and must not change afterwards: two
// answers judged against different prompts are not answers to one item.
func (c *Corpus) Add(group string, conversation []Message, vote Vote, first, second string) error {
	if group == "" {
		return fmt.Errorf("%w: a comparison names no group", ErrCorpus)
	}
	if vote.First == "" || vote.Second == "" || vote.First == vote.Second {
		return fmt.Errorf("%w: group %s compares %q with %q", ErrCorpus, group, vote.First, vote.Second)
	}
	if !slices.Contains([]string{WinnerFirst, WinnerSecond, WinnerTie}, vote.Winner) {
		return fmt.Errorf("%w: group %s: %q is not a verdict", ErrCorpus, group, vote.Winner)
	}
	g, ok := c.groups[group]
	if !ok {
		g = &Group{ID: group, Conversation: conversation, Answers: map[string]string{}}
		c.groups[group] = g
		c.order = append(c.order, group)
	}
	for system, answer := range map[string]string{vote.First: first, vote.Second: second} {
		if _, seen := g.Answers[system]; !seen {
			g.Answers[system] = answer
			g.systems = append(g.systems, system)
		}
	}
	g.Votes = append(g.Votes, vote)
	return nil
}

// Groups returns the groups in the order they were first seen.
func (c *Corpus) Groups() []*Group {
	out := make([]*Group, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.groups[id])
	}
	return out
}

// tally is one pair's human record inside a group.
type tally struct {
	first, second, ties int
	labelers            map[string]bool
}

// key orders a pair so that the two directions of one comparison land in one
// tally.
func key(a, b string) (string, bool) {
	if a < b {
		return a + "\x00" + b, false
	}
	return b + "\x00" + a, true
}

// tallies returns every pair of a group that carries at least one comparison.
func (g *Group) tallies() map[string]*tally {
	out := map[string]*tally{}
	for _, vote := range g.Votes {
		id, swapped := key(vote.First, vote.Second)
		t, ok := out[id]
		if !ok {
			t = &tally{labelers: map[string]bool{}}
			out[id] = t
		}
		winner := vote.Winner
		if swapped {
			switch winner {
			case WinnerFirst:
				winner = WinnerSecond
			case WinnerSecond:
				winner = WinnerFirst
			}
		}
		switch winner {
		case WinnerFirst:
			t.first++
		case WinnerSecond:
			t.second++
		default:
			t.ties++
		}
		if vote.Labeler != "" {
			t.labelers[vote.Labeler] = true
		}
	}
	return out
}

// majority returns which of a and b the people preferred, as one of the two
// system names, or the empty string for a draw. It reports false where the
// pair carries no comparison at all, which is the case a triple is discarded
// for rather than guessed at.
func (g *Group) majority(all map[string]*tally, a, b string) (winner string, votes int, labelers int, ok bool) {
	id, swapped := key(a, b)
	t := all[id]
	if t == nil {
		return "", 0, 0, false
	}
	low, high := a, b
	if swapped {
		low, high = b, a
	}
	switch {
	case t.first > t.second:
		winner = low
	case t.second > t.first:
		winner = high
	}
	return winner, t.first + t.second + t.ties, len(t.labelers), true
}
