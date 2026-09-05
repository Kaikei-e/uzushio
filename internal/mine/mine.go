package mine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// DefaultMinRuns is how many distinct runs a bucket needs before it becomes a
// document. One occurrence is noise, and a vault full of singletons is a vault
// nobody reads.
const DefaultMinRuns = 2

// DefaultMinProposers is how many distinct proposers a bucket needs. One is the
// default because several of the rules are about one proposer by construction —
// a truncated completion is that model's ceiling, not the pool's — and raising
// it would silence them. The research that derived the rules recommends two for
// the rules that are about the pool; the flag reaches it.
const DefaultMinProposers = 1

// Options are the knobs a mining pass takes.
type Options struct {
	// MinRuns is the support threshold in distinct runs. Zero means the
	// default.
	MinRuns int
	// MinProposers is the support threshold in distinct proposers. Zero means
	// the default.
	MinProposers int
	// Instruction reads a task's instruction, for the one rule that makes a
	// claim about it. Nil means the filesystem: the task directory the trace
	// names, which may be gone.
	Instruction func(taskDir string) (string, bool)
}

// withDefaults fills the zero values in.
func (o Options) withDefaults() Options {
	if o.MinRuns <= 0 {
		o.MinRuns = DefaultMinRuns
	}
	if o.MinProposers <= 0 {
		o.MinProposers = DefaultMinProposers
	}
	if o.Instruction == nil {
		o.Instruction = readInstruction
	}
	return o
}

// Bucket is one cluster: every observation of one rule that a single edit could
// plausibly answer together. Clusters are exact agreement on the signature
// (rule, task where the rule is task-scoped, discriminator) — deterministic and
// evidence-grounded, not a similarity measure over prose. Two runs can share a
// status word and have nothing else in common, which is why the status alone is
// never the signature.
type Bucket struct {
	// Rule is the rule that fired.
	Rule *Rule
	// Task is the task the cluster belongs to, empty where the rule is not
	// task-scoped.
	Task string
	// Key is the rule's own discriminator, empty for every rule but the banded
	// one.
	Key string
	// Observations are the firings, sorted by run and then proposer.
	Observations []Observation
	// Runs are the distinct CMoA run identifiers, sorted. This is the cluster
	// size, and it is what the pattern cites as evidence.
	Runs []string
	// Proposers are the distinct proposer identifiers, sorted.
	Proposers []string
}

// First returns the earliest observation in the bucket, which is the cluster's
// representative instance: a run identifier leads with a UTC timestamp, so the
// first observation is the first time the harness did this.
func (b Bucket) First() Observation {
	if len(b.Observations) == 0 {
		return Observation{}
	}
	return b.Observations[0]
}

// Models returns the distinct models the observations name, sorted.
func (b Bucket) Models() []string { return b.distinct(func(o Observation) string { return o.Model }) }

// Tasks returns the distinct tasks the observations name, sorted.
func (b Bucket) Tasks() []string { return b.distinct(func(o Observation) string { return o.Task }) }

// Notes returns the distinct values one note carried, sorted.
func (b Bucket) Notes(key string) []string {
	return b.distinct(func(o Observation) string { return o.Notes[key] })
}

func (b Bucket) distinct(of func(Observation) string) []string {
	values := make([]string, 0, len(b.Observations))
	for _, o := range b.Observations {
		values = append(values, of(o))
	}
	return sortedUnique(values)
}

// PatternID is the identifier the bucket's pattern is written under: the rule's
// fixed slug, joined with the task where the rule is task-scoped and with the
// rule's own discriminator where it has one. It is never a hash — a pattern
// identifier is read by people, and a reader who sees the same failure twice
// should recognise it.
func (b Bucket) PatternID() (string, error) {
	parts := []string{b.Rule.Slug}
	if b.Rule.TaskScoped && b.Task != "" {
		parts = append(parts, slugify(b.Task))
	}
	if b.Key != "" {
		parts = append(parts, slugify(b.Key))
	}
	return vocab.PatternID(strings.Join(parts, "-"))
}

// Result is what one mining pass concluded.
type Result struct {
	// Runs is how many trace runs were read.
	Runs int
	// Buckets are the clusters that met the support thresholds, sorted by
	// pattern identifier.
	Buckets []Bucket
	// Below are the clusters that did not, sorted the same way. They are
	// reported rather than dropped: "three runs short of a pattern" is worth
	// seeing, and it is the honest answer to "why did nothing come out".
	Below []Bucket
	// NotMined reports the failures the rule set deliberately writes no
	// document for, sorted by name. It is a slice rather than a map because it
	// is printed, and a map printed in range order is a report that changes
	// between two passes over the same traces.
	NotMined []NotMinedReport
}

// NotMinedReport is one non-rule's tally.
type NotMinedReport struct {
	// Name labels the non-rule.
	Name string `json:"name"`
	// Reason says why no document is written.
	Reason string `json:"reason"`
	// Count is how many candidates matched.
	Count int `json:"count"`
	// Errors are the distinct error texts, sorted and capped. Without them the
	// tally says a number and nothing about what the number was, which is what
	// makes "count it, log it" worth doing at all.
	Errors []string `json:"errors,omitempty"`
}

// notMinedErrorMax bounds the sample of error texts a report carries.
const notMinedErrorMax = 8

// Mine evaluates every rule over every run and clusters what fires.
func Mine(runs []Run, opts Options) Result {
	opts = opts.withDefaults()
	index := map[string]*Bucket{}
	var order []string
	result := Result{Runs: len(runs)}
	triggers := NotMinedTriggers()
	counts := make([]int, len(triggers))
	texts := make([][]string, len(triggers))
	for i := range runs {
		run := &runs[i]
		in := ruleInput{Run: run, Instruction: opts.Instruction}
		for t, trigger := range triggers {
			for _, c := range run.Candidates {
				if trigger.Match(c) {
					counts[t]++
					texts[t] = append(texts[t], tidyError(c.Error))
				}
			}
		}
		for _, rule := range rules {
			for _, obs := range rule.fire(in) {
				obs.Rule = rule
				key := signature(rule, obs)
				bucket, ok := index[key]
				if !ok {
					task := ""
					if rule.TaskScoped {
						task = obs.Task
					}
					bucket = &Bucket{Rule: rule, Task: task, Key: obs.Key}
					index[key] = bucket
					order = append(order, key)
				}
				bucket.Observations = append(bucket.Observations, obs)
			}
		}
	}
	for _, key := range order {
		bucket := index[key]
		sort.SliceStable(bucket.Observations, func(i, j int) bool {
			a, b := bucket.Observations[i], bucket.Observations[j]
			if a.RunID != b.RunID {
				return a.RunID < b.RunID
			}
			return a.Proposer < b.Proposer
		})
		bucket.Runs = bucket.distinct(func(o Observation) string { return o.RunID })
		bucket.Proposers = bucket.distinct(func(o Observation) string { return o.Proposer })
		if len(bucket.Runs) >= opts.MinRuns && len(bucket.Proposers) >= opts.MinProposers &&
			bucket.smallestPool() >= bucket.Rule.MinPool {
			result.Buckets = append(result.Buckets, *bucket)
		} else {
			result.Below = append(result.Below, *bucket)
		}
	}
	for t, trigger := range triggers {
		if counts[t] == 0 {
			continue
		}
		result.NotMined = append(result.NotMined, NotMinedReport{
			Name: trigger.Name, Reason: trigger.Reason, Count: counts[t],
			Errors: capped(sortedUnique(texts[t]), notMinedErrorMax),
		})
	}
	sort.Slice(result.NotMined, func(i, j int) bool {
		return result.NotMined[i].Name < result.NotMined[j].Name
	})
	byID(result.Buckets)
	byID(result.Below)
	return result
}

// smallestPool is the fewest proposers any run behind this bucket configured.
// A rule that is a claim about the pool is held to it: one run made with a
// single proposer cannot support "others passed" or "nobody else could".
func (b Bucket) smallestPool() int {
	smallest := 0
	for i, o := range b.Observations {
		if i == 0 || o.Pool < smallest {
			smallest = o.Pool
		}
	}
	return smallest
}

// tidyError folds one error text into something a report can list: one line,
// bounded, with no tab or carriage return in it.
func tidyError(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const max = 160
	if len(text) > max {
		text = strings.TrimSpace(text[:max]) + " […]"
	}
	return text
}

// byID sorts buckets by the identifier their pattern would carry, so a report
// and a directory listing read in the same order.
func byID(buckets []Bucket) {
	sort.SliceStable(buckets, func(i, j int) bool {
		left, _ := buckets[i].PatternID()
		right, _ := buckets[j].PatternID()
		return left < right
	})
}

// signature is the cluster key: exact agreement on it is what puts two failures
// in one bucket.
func signature(rule *Rule, obs Observation) string {
	task := ""
	if rule.TaskScoped {
		task = obs.Task
	}
	return rule.Name + "\x00" + task + "\x00" + obs.Key
}

// Pattern renders one bucket as the document that gets written. The context is
// the rule's sentence filled in with what was actually observed, because an
// unsafe control action that restates its own category says nothing about when
// the harness is in trouble.
func (b Bucket) Pattern(day string) (doc.Pattern, error) {
	id, err := b.PatternID()
	if err != nil {
		return doc.Pattern{}, err
	}
	context := b.Rule.context(b)
	if strings.TrimSpace(context) == "" {
		return doc.Pattern{}, fmt.Errorf("mine: rule %s rendered an empty context for %s", b.Rule.Name, id)
	}
	pattern := doc.Pattern{
		PatternID: id,
		Title:     b.Rule.Title,
		Date:      day,
		Status:    vocab.StatusOpen,
		Category:  b.Rule.Category,
		Context:   context,
		Component: b.Rule.Component,
		Evidence:  append([]string(nil), b.Runs...),
		Body:      b.body(),
	}
	if err := pattern.Validate(); err != nil {
		return doc.Pattern{}, err
	}
	return pattern, nil
}

// body says how the failure was found, so a reader can tell the observation
// from the inference. The rule's signal is the observation; the component is
// the inference, and it is the weakest part of any deterministic rule set,
// which is why it is written down as a claim rather than left implicit.
func (b Bucket) body() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Mined from CMoA traces by `uzushio improve`, rule %s.\n\n", b.Rule.Name)
	fmt.Fprintf(&out, "- signal: %s\n", b.Rule.Signal)
	fmt.Fprintf(&out, "- first written from %s on %s, by %s\n",
		countOf(len(b.Runs), "run"), named("task", b.Tasks()), named("proposer", b.Proposers))
	// Every other number a reader might want is derivable from `evidence:`, and
	// deliberately not written here: a later pass appends run identifiers to an
	// open pattern without rewriting the reading somebody may have sharpened,
	// so a count in the prose would be a number that quietly stopped being
	// true. The support is the length of the evidence list.
	out.WriteString("\nThe support is the length of `evidence:`, which later passes grow.\n")
	out.WriteString("\nThe component is where an edit answering this would have to go. It is the\n")
	out.WriteString("rule's attribution and not an observation: the traces say what failed, not\n")
	out.WriteString("which surface was wrong about it.")
	return out.String()
}

// countOf writes a count with its noun, pluralised.
func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// slugPart is what a pattern identifier's segments may hold.
var slugPart = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns an observed word — a task identifier, an invariant name — into
// a segment a pattern identifier can carry.
func slugify(s string) string {
	return strings.Trim(slugPart.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// readInstruction reads a task's instruction from the directory the trace
// named. It answers false rather than failing: a trace outlives the checkout it
// was made from, and a rule that needs the instruction should decline rather
// than stop the pass.
func readInstruction(taskDir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(taskDir, "instruction.md"))
	if err != nil {
		return "", false
	}
	return string(raw), true
}
