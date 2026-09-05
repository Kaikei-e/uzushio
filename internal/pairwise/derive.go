package pairwise

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
)

// The margin strata. A stratum is a band of the eligible items ordered by how
// far the best system is ahead of the second: an item in Wide has an obvious
// answer and an item in Narrow does not.
const (
	StratumWide   = "wide"
	StratumMid    = "mid"
	StratumNarrow = "narrow"
)

// The default mix, as shares of the population and of the sample both. Forty
// per cent obvious, forty per cent middling and twenty per cent close is not a
// property of any corpus: it is the mix that keeps a coefficient readable, by
// refusing both the all-easy set that inflates it and the all-hard set that
// flattens it to zero.
var DefaultStrata = []Stratum{
	{Name: StratumWide, Share: 0.4},
	{Name: StratumMid, Share: 0.4},
	{Name: StratumNarrow, Share: 0.2},
}

// Stratum is one band and the share of the sample it takes.
type Stratum struct {
	Name  string
	Share float64
}

// The three positions a candidate can take in an item. They are the names the
// task directory and the gold file both use, and they are deliberately
// meaningless: the mapping from a position to the system that produced the
// answer is recorded once, in the gold file, and never shown to a judge.
var Positions = []string{"c1", "c2", "c3"}

// GoldTie is the gold label of an item the people did not settle: the three
// majorities are acyclic and leave no single system unbeaten.
const GoldTie = "tie"

// Item is one three-way selection item.
type Item struct {
	// Group is the prompt it came from, Ordinal the index of this triple
	// among the eligible triples of that group.
	Group   string
	Ordinal int
	// Systems is the system behind each position, in c1, c2, c3 order.
	Systems [3]string
	// Answers is each position's answer, verbatim.
	Answers [3]string
	// Conversation is the shared turns, ending with the user turn answered.
	Conversation []Message
	// Gold is the position the human majorities point at, or GoldTie. It is
	// the unique system no majority beat — the source of the acyclic
	// tournament — which is not the same as the system that beat both others.
	Gold string
	// Wins is how many of its two pairs the gold system won outright. Two is a
	// Condorcet winner; one is a system that won a pair and drew the other,
	// and is unbeaten all the same. Zero happens where every pair was drawn,
	// and then Gold is GoldTie.
	Wins int
	// BTTop is the position the Bradley-Terry fit puts first. Where it differs
	// from Gold the item is hard: the transitive model and the raw majorities
	// disagree about it.
	BTTop string
	// Margin is the log-strength gap between the best and the second-best of
	// the three, as the fit sees it.
	Margin float64
	// Stratum is the band Margin put it in.
	Stratum string
	// Judgments is how many human comparisons the three pairs rest on, and
	// Annotators how many distinct people made them.
	Judgments  int
	Annotators int
}

// Hard reports whether the majorities and the fit disagree about which answer
// is best. It is recorded rather than acted on: an item both readings settle
// the same way is easy in a sense that a coefficient should not be allowed to
// hide.
func (i Item) Hard() bool { return i.Gold != GoldTie && i.Gold != i.BTTop }

// Stats is what the derivation measured about the corpus, which is a
// measurement of the human labels rather than of any judge.
type Stats struct {
	Groups int `json:"groups"`
	Votes  int `json:"votes"`
	// Triples is every three-system combination inside a group that could
	// have been an item.
	Triples int `json:"triples"`
	// Incomplete is how many were dropped because a pair carried no human
	// comparison, Cyclic how many were dropped because the three majorities
	// went round in a circle, and Thin how many because they rested on fewer
	// comparisons than asked for.
	Incomplete int `json:"incomplete"`
	Cyclic     int `json:"cyclic"`
	Thin       int `json:"thin"`
	// Eligible is what survived, Sampled what was taken.
	Eligible int `json:"eligible"`
	Sampled  int `json:"sampled"`
	// TieGold is how many of the sampled items left no single system unbeaten;
	// Condorcet how many of the labelled ones were won outright, so a reader
	// can see how much of the label set rests on a drawn pair; and Hard how
	// many the majorities and the fit disagree about.
	TieGold   int `json:"tie_gold"`
	Condorcet int `json:"condorcet"`
	Hard      int `json:"hard"`
	// PerStratum counts the sample by band, PerGroup the number of groups
	// contributing one, two or more items.
	PerStratum map[string]int `json:"per_stratum"`
	GroupsUsed int            `json:"groups_used"`
}

// CycleRate is the share of complete triples whose majorities cycle. It is the
// number to quote as the floor on any judge's agreement with this corpus: the
// items behind it have no true answer, and nothing can be right about them.
func (s Stats) CycleRate() float64 {
	complete := s.Cyclic + s.Eligible + s.Thin
	if complete == 0 {
		return 0
	}
	return float64(s.Cyclic) / float64(complete)
}

// Options are the derivation's knobs.
type Options struct {
	// MinJudgments is the fewest human comparisons a triple may rest on,
	// counted over its three pairs. Below three a triple cannot even have one
	// comparison per pair.
	MinJudgments int
	// Target is how many items to sample. Zero takes everything eligible.
	Target int
	// Seed fixes the position permutation and the sampling. The same seed over
	// the same corpus writes the same suite.
	Seed uint64
	// Strata is the mix. Nil is DefaultStrata.
	Strata []Stratum
}

// Derive turns a corpus into a stratified sample of three-way items.
func Derive(c *Corpus, opts Options) ([]Item, Stats, error) {
	if opts.MinJudgments < 3 {
		opts.MinJudgments = 3
	}
	strata := opts.Strata
	if len(strata) == 0 {
		strata = DefaultStrata
	}
	stats := Stats{PerStratum: map[string]int{}}
	var eligible []Item
	for _, g := range c.Groups() {
		stats.Groups++
		stats.Votes += len(g.Votes)
		items, groupStats, err := deriveGroup(g, opts)
		if err != nil {
			return nil, Stats{}, err
		}
		stats.Triples += groupStats.Triples
		stats.Incomplete += groupStats.Incomplete
		stats.Cyclic += groupStats.Cyclic
		stats.Thin += groupStats.Thin
		eligible = append(eligible, items...)
	}
	stats.Eligible = len(eligible)
	sample := stratify(eligible, strata, opts.Target, opts.Seed)
	place(sample, opts.Seed)

	groups := map[string]bool{}
	for _, item := range sample {
		stats.PerStratum[item.Stratum]++
		groups[item.Group] = true
		if item.Gold == GoldTie {
			stats.TieGold++
		}
		if item.Wins == 2 {
			stats.Condorcet++
		}
		if item.Hard() {
			stats.Hard++
		}
	}
	stats.Sampled = len(sample)
	stats.GroupsUsed = len(groups)
	return sample, stats, nil
}

// deriveGroup builds every eligible item of one prompt.
func deriveGroup(g *Group, opts Options) ([]Item, Stats, error) {
	var stats Stats
	systems := slices.Clone(g.systems)
	sort.Strings(systems)
	if len(systems) < 3 {
		return nil, stats, nil
	}
	all := g.tallies()
	strength := bradleyTerry(systems, all)

	var out []Item
	ordinal := 0
	for i := range systems {
		for j := i + 1; j < len(systems); j++ {
			for k := j + 1; k < len(systems); k++ {
				stats.Triples++
				triple := [3]string{systems[i], systems[j], systems[k]}
				item, kind := deriveTriple(g, all, strength, triple, opts.MinJudgments)
				switch kind {
				case tripleIncomplete:
					stats.Incomplete++
					continue
				case tripleCyclic:
					stats.Cyclic++
					continue
				case tripleThin:
					stats.Thin++
					continue
				}
				item.Ordinal = ordinal
				ordinal++
				out = append(out, item)
			}
		}
	}
	return out, stats, nil
}

// The reasons a triple is not an item.
const (
	tripleOK = iota
	tripleIncomplete
	tripleCyclic
	tripleThin
)

// deriveTriple settles one three-system combination.
func deriveTriple(g *Group, all map[string]*tally, strength map[string]float64,
	triple [3]string, minJudgments int,
) (Item, int) {
	wins, losses := map[string]int{}, map[string]int{}
	votes := 0
	for a := range 3 {
		for b := a + 1; b < 3; b++ {
			winner, n, _, ok := g.majority(all, triple[a], triple[b])
			if !ok {
				return Item{}, tripleIncomplete
			}
			votes += n
			if winner == "" {
				continue
			}
			wins[winner]++
			loser := triple[a]
			if winner == loser {
				loser = triple[b]
			}
			losses[loser]++
		}
	}
	if votes < minJudgments {
		return Item{}, tripleThin
	}
	// A three-node tournament cycles exactly when every node has one win, so
	// the check is a count rather than a graph walk.
	if len(wins) == 3 {
		return Item{}, tripleCyclic
	}
	// The gold label is the source of the acyclic tournament: the one system
	// no majority beat. That is a weaker condition than beating both others,
	// and the difference is a third of the corpus — an item where A beat B,
	// B beat C and A drew with C has an unbeaten system, and calling it
	// undecided puts a label the people did not give into the human marginal
	// of every validity table. `tie` is reserved for a triple with no unique
	// unbeaten system, which is what "the people left this open" means.
	gold, goldWins := GoldTie, 0
	unbeaten := make([]string, 0, 3)
	for _, system := range triple {
		if losses[system] == 0 {
			unbeaten = append(unbeaten, system)
		}
	}
	if len(unbeaten) == 1 {
		gold = unbeaten[0]
		goldWins = wins[gold]
	}

	ordered := slices.Clone(triple[:])
	sort.Slice(ordered, func(x, y int) bool { return strength[ordered[x]] > strength[ordered[y]] })
	margin := math.Log(strength[ordered[0]]) - math.Log(strength[ordered[1]])

	item := Item{
		Group:        g.ID,
		Systems:      triple,
		Conversation: g.Conversation,
		Gold:         gold,
		Wins:         goldWins,
		BTTop:        ordered[0],
		Margin:       margin,
		Judgments:    votes,
		Annotators:   maxLabelers(g, all, triple),
	}
	for n, system := range triple {
		item.Answers[n] = g.Answers[system]
	}
	return item, tripleOK
}

// maxLabelers is the largest number of distinct people who judged any one pair
// of the triple. It is a lower bound on how many people the item rests on,
// which is the honest thing to record: the corpus says who judged each pair
// and not who judged the triple, and a sum would count one person three times.
func maxLabelers(g *Group, all map[string]*tally, triple [3]string) int {
	most := 0
	for a := range 3 {
		for b := a + 1; b < 3; b++ {
			if _, _, people, ok := g.majority(all, triple[a], triple[b]); ok && people > most {
				most = people
			}
		}
	}
	return most
}

// btPrior is the pseudo-comparison added to every pair of a group before the
// fit. Without it a system that lost everything has a strength of zero and a
// margin of infinity, and a group whose tournament is not strongly connected
// has no fit at all. Half a win each way is the least opinionated repair that
// keeps every strength finite.
const btPrior = 0.5

// bradleyTerry fits the strengths of a group's systems by the standard MM
// iteration: p_i <- W_i / sum_j n_ij/(p_i + p_j), normalised so the strengths
// have geometric mean one.
//
// It is fitted beside the majority rule rather than instead of it. BT assumes
// the comparisons are transitive, so it will happily rank a cyclic triple; the
// derivation uses the fit only for the margin, and records where the two
// readings disagree.
func bradleyTerry(systems []string, all map[string]*tally) map[string]float64 {
	const iterations = 200
	// The comparison counts are held in an indexed matrix rather than in
	// nested maps, and every sum below runs over the sorted slice. Floating
	// point addition is not associative, so a map's iteration order would make
	// the fitted strengths differ in the last bits between two runs over one
	// corpus — and the margins those strengths produce are what the sampling
	// sorts on, so a run would sample a different suite each time. A seed that
	// does not fix the output is not a seed.
	index := map[string]int{}
	for i, s := range systems {
		index[s] = i
	}
	wins := make([]float64, len(systems))
	pairs := make([][]float64, len(systems))
	for i := range pairs {
		pairs[i] = make([]float64, len(systems))
	}
	for a := range systems {
		for b := a + 1; b < len(systems); b++ {
			low, high := systems[a], systems[b]
			t := all[low+"\x00"+high]
			first, second, ties := btPrior, btPrior, 0.0
			if t != nil {
				first += float64(t.first)
				second += float64(t.second)
				ties = float64(t.ties)
			}
			// A tie is half a win each way, which is what a Bradley-Terry fit
			// can represent of one: the model has no third outcome.
			wins[index[low]] += first + ties/2
			wins[index[high]] += second + ties/2
			n := first + second + ties
			pairs[index[low]][index[high]] = n
			pairs[index[high]][index[low]] = n
		}
	}
	strength := make([]float64, len(systems))
	next := make([]float64, len(systems))
	for i := range strength {
		strength[i] = 1
	}
	for range iterations {
		for i := range systems {
			denominator := 0.0
			for j := range systems {
				if i == j || pairs[i][j] == 0 {
					continue
				}
				denominator += pairs[i][j] / (strength[i] + strength[j])
			}
			if denominator == 0 || wins[i] == 0 {
				next[i] = strength[i]
				continue
			}
			next[i] = wins[i] / denominator
		}
		// Normalise to a geometric mean of one, so the strengths of two groups
		// are on one scale and a margin is a log-odds difference.
		logSum := 0.0
		for i := range systems {
			logSum += math.Log(next[i])
		}
		scale := math.Exp(-logSum / float64(len(systems)))
		for i := range systems {
			strength[i] = next[i] * scale
		}
	}
	out := make(map[string]float64, len(systems))
	for i, s := range systems {
		out[s] = strength[i]
	}
	return out
}

// stratify bands the eligible items by margin and samples the mix.
//
// The bands are the population's own quantiles at the mix's shares, so the
// sample's mix is the population's mix by construction and the allocation is
// proportional. What stratifying buys over a plain random sample is that the
// mix is exact rather than expected — at two hundred items a random sample's
// narrow band can come out half the size it should be, and the narrow band is
// the one the coefficient is most sensitive to.
func stratify(items []Item, strata []Stratum, target int, seed uint64) []Item {
	if len(items) == 0 {
		return nil
	}
	ordered := slices.Clone(items)
	// Descending margin: the widest gaps first. The group and ordinal break
	// ties so the order does not depend on the sort's stability.
	sort.Slice(ordered, func(a, b int) bool {
		if ordered[a].Margin != ordered[b].Margin {
			return ordered[a].Margin > ordered[b].Margin
		}
		if ordered[a].Group != ordered[b].Group {
			return ordered[a].Group < ordered[b].Group
		}
		return ordered[a].Ordinal < ordered[b].Ordinal
	})

	bands := make([][]Item, len(strata))
	at := 0
	for i, stratum := range strata {
		size := int(math.Round(stratum.Share * float64(len(ordered))))
		if i == len(strata)-1 {
			size = len(ordered) - at
		}
		size = min(max(size, 0), len(ordered)-at)
		for j := at; j < at+size; j++ {
			ordered[j].Stratum = stratum.Name
		}
		bands[i] = ordered[at : at+size]
		at += size
	}

	if target <= 0 || target >= len(ordered) {
		out := make([]Item, 0, len(ordered))
		for _, band := range bands {
			out = append(out, band...)
		}
		return out
	}

	// One generator, drawn from in band order, so the sample is a function of
	// the seed and the corpus and of nothing else.
	source := rand.New(rand.NewPCG(seed, 0x75_7a_75_73_68_69_6f_00))
	var out []Item
	taken := 0
	for i, stratum := range strata {
		want := int(math.Round(stratum.Share * float64(target)))
		if i == len(strata)-1 {
			want = target - taken
		}
		band := slices.Clone(bands[i])
		source.Shuffle(len(band), func(a, b int) { band[a], band[b] = band[b], band[a] })
		want = min(max(want, 0), len(band))
		out = append(out, band[:want]...)
		taken += want
	}
	return out
}

// place assigns each item's three systems to the three positions, by a
// permutation drawn from the seed.
//
// It is what makes the position meaningless, which is what lets the same items
// be reused for a position-bias measurement: a judge that prefers whatever is
// shown first is measured against a corpus where being first says nothing
// about being right.
func place(items []Item, seed uint64) {
	source := rand.New(rand.NewPCG(seed, 0x70_6f_73_69_74_69_6f_6e))
	for i := range items {
		order := []int{0, 1, 2}
		source.Shuffle(3, func(a, b int) { order[a], order[b] = order[b], order[a] })
		var systems [3]string
		var answers [3]string
		position := map[string]string{}
		for n, from := range order {
			systems[n] = items[i].Systems[from]
			answers[n] = items[i].Answers[from]
			position[systems[n]] = Positions[n]
		}
		items[i].Systems = systems
		items[i].Answers = answers
		if items[i].Gold != GoldTie {
			items[i].Gold = position[items[i].Gold]
		}
		items[i].BTTop = position[items[i].BTTop]
	}
}
