package mine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// A rule is one deterministic reading of a trace: a predicate over JSON fields,
// the unsafe control action it names, and the surface an edit answering it would
// have to change. There is no model in the loop and there is no similarity
// measure — clusters are exact agreement on a signature, which is what makes a
// second mining pass over the same traces produce the same documents.
//
// STPA is about the harness's control action, not about the model's answer. The
// controller is the harness; the control action is what the harness gives the
// proposer — an instruction, a file, a format rule, a time budget. Every
// category below is chosen by reading the failure that way, and a rule that
// cannot be read that way is not a rule (see NotMinedTriggers).
//
// # Where the component boundary is drawn, and why
//
// Component attribution is the weakest part of any deterministic rule set, and
// the table below is deliberate about one line in it. The research that derived
// these rules attributed every diff-format failure to `system-prompt`, because
// that is where the output contract lived when there was no harness directory
// to put it anywhere else — and then observed that `system-prompt` is the one
// surface an ablation measured as *regressing*, and the one CMoA declares
// `human-approval`. A rule set that sends every mineable failure there aims the
// loop at its least productive surface and puts a person in front of every one.
//
// Now that a harness directory exists, the research's own prose prescribes the
// move for R3-R5: the apply-failure rules attribute to `memory`, an
// `auto-accept` surface, because a note saying "quote the file bytes" is a
// thing a note can say and the loop can close on it without asking anyone.
//
// The line stops there, and this is where it is written down. R1, R10, R11 and
// R12 stay on `system-prompt` because each is a failure of the *contract* the
// prompt states rather than of knowledge the agent lacked: a completion with no
// diff in it at all, a completion cut off at the token ceiling, a diff editing a
// file the task did not list, a diff rewriting the verifier's own tests. A note
// can teach a format; it cannot impose a constraint the contract never stated.
// Moving them would be a second judgement about which surface is productive,
// and it belongs in a decision record rather than in a mining rule.
type Rule struct {
	// Name is the rule's label, as the research that derived it numbers them.
	// It is what a report line and a bug report can both point at.
	Name string
	// Slug is the pattern identifier's stem, so the same failure always lands
	// on the same document. A pattern id is read by people, so it is a fixed
	// word rather than a hash of the observation.
	Slug string
	// Title is the pattern's heading: the failure named as what goes wrong.
	Title string
	// Category is the unsafe control action type.
	Category vocab.Category
	// Component is the harness surface an edit would have to change.
	Component string
	// TaskScoped says the failure belongs to one task, so the task's
	// identifier joins the pattern's. A rule that is not task-scoped is about
	// the harness whatever it is pointed at, and buckets across tasks.
	TaskScoped bool
	// Signal is one line saying which fields the predicate reads. It goes in
	// the pattern's body, so a reader can tell what was actually observed from
	// what was inferred.
	Signal string
	// MinPool is how many proposers the runs behind this pattern must have had.
	// A rule that is a claim about the *pool* — some proposers failed where
	// others passed, exactly one ever passes, nobody could do it — says nothing
	// on a corpus run with one proposer, whatever the caller's thresholds are.
	// It is a floor on the pool the run configured, not on the proposers the
	// observations are attributed to: R13 fires on the single proposer that
	// passed, and its claim is precisely that the others did not.
	//
	// A rule about one model's own ceiling carries none.
	MinPool int

	// fire evaluates the predicate over one run.
	fire func(in ruleInput) []Observation
	// context renders the required STPA context from the bucket the rule's
	// observations landed in. An unsafe control action that names no context
	// is a component and a verb, so this never returns an empty string.
	context func(b Bucket) string
}

// ruleInput is one run plus the little the rules need from outside it.
type ruleInput struct {
	Run *Run
	// Instruction returns the task's instruction text. It answers false where
	// the task directory the trace names is gone, which is the ordinary case
	// for a trace read on another machine — a rule that needs the instruction
	// then does not fire, rather than guessing.
	Instruction func(taskDir string) (string, bool)
}

// Observation is one firing of one rule: which run and which proposer, the
// discriminator that separates two failures of the same rule that no single
// edit could fix together, and the facts a context sentence reads.
type Observation struct {
	Rule     *Rule
	RunID    string
	Task     string
	Proposer string
	Model    string
	// Key discriminates buckets beyond the rule and the task. It is empty for
	// most rules; the banded one keys on the invariant, because two invariants
	// breaching their bands are two patterns and one edit fixes neither of the
	// other's, and the rejected-request one keys on the reason.
	Key string
	// Pool is how many proposers the run configured. It is what the rules that
	// are claims about the pool are held to, rather than the number of
	// proposers that happened to be observed.
	Pool int
	// Notes are the observation's own facts, read back by the rule's context
	// function. They are strings because a context sentence is a string, and a
	// number that went through a float would come back as a value no reader
	// matches.
	Notes map[string]string
}

// note returns one fact, or the empty string.
func (o Observation) note(key string) string { return o.Notes[key] }

// The apply_error texts `git apply` writes. Its messages are a small closed
// set, which is what makes reading them a predicate rather than a heuristic.
var (
	doesNotApply  = regexp.MustCompile(`patch does not apply`)
	corruptPatch  = regexp.MustCompile(`corrupt patch at line`)
	outOfScope    = regexp.MustCompile(`already exists|No such file or directory|new file`)
	failedFileMsg = regexp.MustCompile(`(?m)^error: ([^:\n]+): patch does not apply$`)
	testFile      = regexp.MustCompile(`_test\.go$`)
	// asksForTests matches the word, not the letters. "the latest version",
	// "greatest", "protest" and "contest" all carry the substring, and a rule
	// that reads them as consent is a rule that never fires.
	asksForTests = regexp.MustCompile(`(?i)\btests?\b|_test\.go`)
)

// applyOrder is the three apply-failure rules as a decision list, most specific
// reading first. `git apply` prints several `error:` lines for one failure — a
// path outside the worktree produces both "No such file or directory" and
// "patch does not apply" — so evaluating the three as independent predicates
// would count one failure toward two thresholds and write two patterns for it.
// One failure is one reading.
var applyOrder = []struct {
	name    string
	message *regexp.Regexp
}{
	{"R3", doesNotApply},
	{"R4", corruptPatch},
	{"R5", outOfScope},
}

// httpStatus reads the code out of the text CMoA writes for a non-2xx answer,
// which is `llm: HTTP <code>: <body>`.
//
// The discrimination between "the server was unreachable" and "the server read
// the request and refused it" is made on this code and not on the error's
// prose. A transport failure carries no status at all — it is a Go dial or
// read error — so the presence of a status is the whole test, and it does not
// go stale the way an alternation of `connection refused|no such host|EOF|…`
// would as Go's net package rewords itself.
var httpStatus = regexp.MustCompile(`HTTP (\d{3})`)

// The body texts that say a rejection was about the size of the prompt rather
// than about anything else. A server spells the same refusal several ways, so
// the set is a small alternation read once and turned into one reason word.
var contextOverflow = regexp.MustCompile(
	`(?i)n_ctx|context length|context_length|context window|maximum context|` +
		`too many tokens|max_tokens|prompt is too long|exceeds`)

// Rules returns the rule set, in the order the research numbers it. The order
// is the report's order and nothing else depends on it: two rules never
// contend, because each writes its own pattern.
func Rules() []*Rule { return rules }

// RuleByName returns one rule by its label.
func RuleByName(name string) (*Rule, bool) {
	for _, r := range rules {
		if r.Name == name {
			return r, true
		}
	}
	return nil, false
}

// NotMined is a trigger that deliberately writes no pattern, with the reason.
// It is counted and its error texts are kept, so that a failure nobody may act
// on is still visible; it is not a document because a pattern no edit can fix
// is one pattern_resolved_unfixed would nag about forever.
type NotMined struct {
	// Name labels the non-rule.
	Name string
	// Reason says why no document is written.
	Reason string
	// Match reports whether one candidate is the case meant. It reads the whole
	// candidate rather than its status word: the status alone cannot tell a
	// server that was unreachable from a server that read the request and said
	// no, and only one of those is outside every harness surface.
	Match func(c Candidate) bool
}

// NotMinedTriggers are the failures that are counted and never written down.
//
// There is exactly one, and it is narrower than the status word. Research §E.3
// justified declining every `http_error` on a corpus in which all six of them
// were `connection refused` — but the same status also carries a 4xx from the
// model server, which is the server reading a request the harness built and
// refusing it. That is a harness control failure and R16 mines it.
func NotMinedTriggers() []NotMined {
	return []NotMined{
		{
			Name: "R15",
			Reason: "the model server could not be reached, or answered 5xx: an " +
				"infrastructure fact rather than a harness control failure, which no edit " +
				"to any surface can fix",
			Match: func(c Candidate) bool {
				return c.Status == CandidateHTTPError && !rejectedRequest(c)
			},
		},
	}
}

// rejectedRequest reports whether an http_error is the server refusing a
// request the harness built — a 4xx — rather than the server being unreachable
// or broken. A 5xx is the server's own failure and stays with R15.
func rejectedRequest(c Candidate) bool {
	if c.Status != CandidateHTTPError {
		return false
	}
	match := httpStatus.FindStringSubmatch(c.Error)
	if match == nil {
		// No status at all: a transport error — a refused dial, a reset, a name
		// that would not resolve — or something CMoA could not classify. Either
		// way the harness did not build a request a server read and refused.
		return false
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return false
	}
	// 4xx and nothing else. A 5xx is the server's own failure and stays with
	// R15, where it is counted and no document is written for it.
	return code >= 400 && code <= 499
}

// rejectionReason turns one 4xx into the word its bucket is named for. Two
// refusals with different reasons are two failures: an edit that shortens the
// prompt answers a context overflow and does nothing about a rate limit.
func rejectionReason(c Candidate) (code, reason string) {
	match := httpStatus.FindStringSubmatch(c.Error)
	if match == nil {
		return "", "unknown"
	}
	code = match[1]
	switch {
	case contextOverflow.MatchString(c.Error):
		return code, "context-length"
	case code == "413":
		return code, "payload-too-large"
	case code == "429":
		return code, "rate-limited"
	case code == "401" || code == "403":
		return code, "not-authorised"
	}
	return code, "http-" + code
}

var rules = []*Rule{
	{
		Name:       "R1",
		Slug:       "no-diff-emitted",
		Title:      "A completion arrives with no unified diff in it",
		Category:   vocab.CategoryNotProvided,
		Component:  "system-prompt",
		TaskScoped: false,
		Signal:     `candidates/<id>.json status == "no_diff"`,
		fire: func(in ruleInput) []Observation {
			return candidateStatus(in.Run, CandidateNoDiff, func(c Candidate) map[string]string {
				return map[string]string{"prompt_version": in.Run.PromptVersion}
			})
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("%s on %s, prompt_version %s; a completion arrived with no unified diff in it",
				named("proposer", b.Models()), named("task", b.Tasks()), list(b.Notes("prompt_version")))
		},
	},
	{
		Name:       "R2",
		Slug:       "non-completion-response",
		Title:      "A 2xx response that is not a chat completion",
		Category:   vocab.CategoryNotProvided,
		Component:  "tool-description",
		TaskScoped: false,
		Signal:     `candidates/<id>.json status == "malformed"`,
		fire: func(in ruleInput) []Observation {
			return candidateStatus(in.Run, CandidateMalformed, nil)
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("%s on %s; a 2xx response that is not a chat completion",
				named("proposer", b.Models()), named("task", b.Tasks()))
		},
	},
	{
		Name:       "R3",
		Slug:       "context-lines-drift",
		Title:      "The diff's context lines do not match the file bytes the prompt supplied",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "memory",
		TaskScoped: true,
		Signal:     `verify/<id>/result.json status == "apply_failed" and apply_error matches "patch does not apply"`,
		fire: func(in ruleInput) []Observation {
			return applyFailure(in.Run, "R3")
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s, %s; the diff's context lines do not match the file bytes the prompt supplied",
				b.Task, named("file", b.Notes("file")))
		},
	},
	{
		Name:       "R4",
		Slug:       "malformed-hunk",
		Title:      "A hunk header or line prefix violates the unified-diff grammar",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "memory",
		TaskScoped: true,
		Signal:     `verify/<id>/result.json status == "apply_failed" and apply_error matches "corrupt patch at line"`,
		fire: func(in ruleInput) []Observation {
			return applyFailure(in.Run, "R4")
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; a hunk header or line prefix violates the unified-diff grammar, "+
				"so %s never reached the verifier", b.Task, named("proposer", b.Models()))
		},
	},
	{
		Name:       "R5",
		Slug:       "out-of-scope-path",
		Title:      "The diff names a path the task did not put in the worktree",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "memory",
		TaskScoped: true,
		Signal: `verify/<id>/result.json status == "apply_failed" and apply_error matches ` +
			`"already exists|No such file or directory|new file"`,
		fire: func(in ruleInput) []Observation {
			return applyFailure(in.Run, "R5")
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; the diff names a path outside the task's files, or carries the "+
				"wrong a/ b/ prefix, so it did not apply", b.Task)
		},
	},
	{
		Name: "R6",
		Slug: "task-specific-knowledge-gap",
		// A claim about the pool needs a pool: on one proposer it says nothing.
		MinPool:    2,
		Title:      "Some proposers fail the verifier on a prompt others pass",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "memory",
		TaskScoped: true,
		Signal: `verify/<id>/result.json status == "fail" with a non-zero exit code, ` +
			`while another proposer in the same run passed`,
		fire:    fireDisagreement,
		context: contextDisagreement,
	},
	{
		Name: "R7",
		Slug: "instruction-underspecified",
		// A claim about the pool needs a pool: on one proposer it says nothing.
		MinPool:    2,
		Title:      "No proposer produces a passing patch from what the task gives",
		Category:   vocab.CategoryNotProvided,
		Component:  "memory",
		TaskScoped: true,
		Signal: `select.json selection.kind == "no_candidate", every ok candidate verified ` +
			`"fail", and none of them failed to apply`,
		fire: fireNoCandidate,
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; no proposer produced a passing patch from the instruction "+
				"and files given", b.Task)
		},
	},
	{
		Name:       "R8",
		Slug:       "proposer-budget-exceeded",
		Title:      "A proposer's request does not finish inside its budget",
		Category:   vocab.CategoryWrongDuration,
		Component:  "middleware",
		TaskScoped: false,
		Signal:     `candidates/<id>.json status == "timeout"`,
		fire: func(in ruleInput) []Observation {
			return candidateStatus(in.Run, CandidateTimeout, func(c Candidate) map[string]string {
				notes := map[string]string{"prompt_tokens": strconv.Itoa(c.PromptTokens)}
				if budget := in.Run.ProposerTimeoutSeconds(c.ProposerID); budget > 0 {
					notes["budget_ms"] = strconv.Itoa(budget * 1000)
				}
				return notes
			})
		},
		context: func(b Bucket) string {
			budget := "the configured budget"
			if got := b.Notes("budget_ms"); len(got) > 0 {
				budget = span(got) + "ms"
			}
			return fmt.Sprintf("%s on %s, request budget %s, prompt %s tokens; the request did not finish",
				named("proposer", b.Models()), named("task", b.Tasks()), budget, span(b.Notes("prompt_tokens")))
		},
	},
	{
		Name:       "R9",
		Slug:       "verify-budget-exceeded",
		Title:      "The verifier does not finish inside its budget",
		Category:   vocab.CategoryWrongDuration,
		Component:  "middleware",
		TaskScoped: true,
		Signal:     `verify/<id>/result.json status == "timeout"`,
		fire: func(in ruleInput) []Observation {
			var out []Observation
			for _, c := range in.Run.Candidates {
				v, ok := in.Run.Verifies[c.ProposerID]
				if !ok || v.Status != VerifyTimeout {
					continue
				}
				notes := map[string]string{}
				if in.Run.VerifyTimeoutSeconds > 0 {
					notes["timeout_s"] = strconv.Itoa(in.Run.VerifyTimeoutSeconds)
				}
				out = append(out, observe(in.Run, c, notes))
			}
			return out
		},
		context: func(b Bucket) string {
			budget := "the configured budget"
			if got := b.Notes("timeout_s"); len(got) > 0 {
				budget = span(got) + "s"
			}
			return fmt.Sprintf("task %s, verify timeout %s; the verifier did not finish", b.Task, budget)
		},
	},
	{
		Name:       "R10",
		Slug:       "completion-truncated",
		Title:      "The completion hits the token ceiling and the diff is cut off",
		Category:   vocab.CategoryWrongDuration,
		Component:  "system-prompt",
		TaskScoped: false,
		Signal:     `candidates/<id>.json status == "ok" and finish_reason == "length"`,
		fire: func(in ruleInput) []Observation {
			var out []Observation
			for _, c := range in.Run.Candidates {
				if c.Status != CandidateOK || c.FinishReason != "length" {
					continue
				}
				out = append(out, observe(in.Run, c, map[string]string{
					"completion_tokens": strconv.Itoa(c.CompletionTokens),
				}))
			}
			return out
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("%s on %s; the completion hit the token ceiling at %s tokens and the diff "+
				"is truncated", named("proposer", b.Models()), named("task", b.Tasks()),
				span(b.Notes("completion_tokens")))
		},
	},
	{
		Name:       "R11",
		Slug:       "edits-unlisted-file",
		Title:      "The diff edits a file the task did not list",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "system-prompt",
		TaskScoped: true,
		Signal:     `candidates/<id>.json diff.files is not a subset of run.json task.files`,
		fire:       fireUnlistedFile,
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; the diff edits %s, which the task did not list",
				b.Task, list(b.Notes("extra")))
		},
	},
	{
		Name:       "R12",
		Slug:       "edits-the-tests",
		Title:      "The diff modifies the verifier's own tests",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "system-prompt",
		TaskScoped: true,
		Signal: `candidates/<id>.json diff.files includes a path matching _test\.go$ ` +
			`and the task's instruction does not mention tests`,
		fire: fireEditsTests,
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; the diff modifies %s, which is the verifier's own tests and which "+
				"the instruction never asked for", b.Task, list(b.Notes("file")))
		},
	},
	{
		Name: "R13",
		Slug: "single-proposer-dependence",
		// A claim about the pool needs a pool: on one proposer it says nothing.
		MinPool:    2,
		Title:      "Exactly one proposer ever passes, so the pool has no redundancy here",
		Category:   vocab.CategoryNotProvided,
		Component:  "subagent-config",
		TaskScoped: true,
		Signal:     `select.json selection.kind == "selected" and also_passed is empty`,
		fire:       fireSingleProposer,
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s; exactly one proposer ever passes — the pool provides no "+
				"redundancy here, so %s failing is the task failing",
				b.Task, named("proposer", b.Models()))
		},
	},
	{
		Name:       "R14",
		Slug:       "persistent-band-breach",
		Title:      "The same invariant breaches its band run after run",
		Category:   vocab.CategoryWrongTiming,
		Component:  "middleware",
		TaskScoped: true,
		Signal:     `verify/<id>/result.json band.failed names the same invariant across runs`,
		fire:       fireBandBreach,
		context: func(b Bucket) string {
			return fmt.Sprintf("task %s, invariant %s; %s",
				b.Task, b.Key, measurement(b))
		},
	},
	{
		// R16 is not in the research's table. It is the half of R15 that turned
		// out to be mineable: the corpus §E.3 read had six http_errors and all
		// six were `connection refused`, so declining the whole status looked
		// free. A 4xx is not that. It is the server reading a request the
		// harness built and refusing it — most often because the prompt the
		// harness poured its surfaces into did not fit the context window —
		// and that is a control action of the harness's own, on the surface
		// that decides how much goes into a prompt.
		Name:       "R16",
		Slug:       "request-rejected-by-server",
		Title:      "The model server refuses the request the harness built",
		Category:   vocab.CategoryUnsafeProvided,
		Component:  "system-prompt",
		TaskScoped: false,
		Signal:     `candidates/<id>.json status == "http_error" and error carries an HTTP 4xx`,
		fire: func(in ruleInput) []Observation {
			var out []Observation
			for _, c := range in.Run.Candidates {
				if !rejectedRequest(c) {
					continue
				}
				code, reason := rejectionReason(c)
				obs := observe(in.Run, c, map[string]string{
					"status": code,
					"reason": reason,
					"tokens": strconv.Itoa(c.PromptTokens),
				})
				// One reason is one bucket: an edit that shortens the prompt
				// answers a context overflow and does nothing about a rate
				// limit, so the two must not share a threshold.
				obs.Key = reason
				out = append(out, obs)
			}
			return out
		},
		context: func(b Bucket) string {
			return fmt.Sprintf("%s on %s; the model server answered HTTP %s (%s) to the request the "+
				"harness built, so nothing was proposed",
				named("proposer", b.Models()), named("task", b.Tasks()),
				span(b.Notes("status")), b.Key)
		},
	},
}

// candidateStatus is the shape of the rules whose whole predicate is one
// candidate status word.
func candidateStatus(run *Run, status string, notes func(Candidate) map[string]string) []Observation {
	var out []Observation
	for _, c := range run.Candidates {
		if c.Status != status {
			continue
		}
		var extra map[string]string
		if notes != nil {
			extra = notes(c)
		}
		out = append(out, observe(run, c, extra))
	}
	return out
}

// applyFailure is the shape of the three rules that read `git apply`'s stderr,
// evaluated as a decision list: one failure fires the first rule in applyOrder
// whose message it carries, and no other. The file is named where the message
// names it, because "which file" is the difference between an edit that could
// fix the drift and one that could not.
func applyFailure(run *Run, name string) []Observation {
	var out []Observation
	for _, c := range run.Candidates {
		v, ok := run.Verifies[c.ProposerID]
		if !ok || v.Status != VerifyApplyFailed || applyReading(v.ApplyError) != name {
			continue
		}
		notes := map[string]string{}
		if files := failedFiles(v.ApplyError, c.DiffFiles); len(files) > 0 {
			notes["file"] = strings.Join(files, ", ")
		}
		out = append(out, observe(run, c, notes))
	}
	return out
}

// applyReading returns the one rule an apply failure is read by, or the empty
// string where it carries none of the three messages.
func applyReading(applyError string) string {
	for _, entry := range applyOrder {
		if entry.message.MatchString(applyError) {
			return entry.name
		}
	}
	return ""
}

// failedFiles reads the paths `git apply` named, falling back to the paths the
// diff claimed to touch.
func failedFiles(applyError string, diffFiles []string) []string {
	var found []string
	for _, match := range failedFileMsg.FindAllStringSubmatch(applyError, -1) {
		found = append(found, match[1])
	}
	if len(found) == 0 {
		found = append(found, diffFiles...)
	}
	return sortedUnique(found)
}

// fireDisagreement is R6: proposers disagreeing about the same prompt bytes.
// The disagreement is the signal — every candidate is verified even after the
// first pass, so a run says who else could do it, and a failure others did not
// share is not the task's.
func fireDisagreement(in ruleInput) []Observation {
	var failed, passed []Candidate
	for _, c := range in.Run.Candidates {
		v, ok := in.Run.Verifies[c.ProposerID]
		if !ok {
			continue
		}
		switch {
		case v.Status == VerifyFail && v.ExitCode != 0:
			failed = append(failed, c)
		case v.Status == VerifyPass:
			passed = append(passed, c)
		}
	}
	if len(failed) == 0 || len(passed) == 0 {
		return nil
	}
	notes := map[string]string{
		"n_fail": strconv.Itoa(len(failed)),
		"n_pass": strconv.Itoa(len(passed)),
		"n":      strconv.Itoa(len(in.Run.Candidates)),
	}
	var out []Observation
	for _, c := range failed {
		out = append(out, observe(in.Run, c, notes))
	}
	return out
}

// contextDisagreement renders R6 from the run the bucket saw first, which is
// its earliest: a run identifier leads with a timestamp, so the representative
// instance is the first time the harness did this.
func contextDisagreement(b Bucket) string {
	first := b.First()
	return fmt.Sprintf("task %s; %s of %s proposers failed the verifier on a prompt %s others passed",
		b.Task, first.note("n_fail"), first.note("n"), first.note("n_pass"))
}

// fireNoCandidate is R7: every proposer that produced a diff had it applied and
// rejected. An apply failure anywhere in the run disqualifies it, because then
// the run says something about the diff format and R3-R5 have already said it.
func fireNoCandidate(in ruleInput) []Observation {
	if in.Run.Selection.Kind != SelectionNoCandidate {
		return nil
	}
	var ok []Candidate
	for _, c := range in.Run.Candidates {
		if c.Status != CandidateOK {
			continue
		}
		v, found := in.Run.Verifies[c.ProposerID]
		if !found || v.Status == VerifyApplyFailed {
			return nil
		}
		if v.Status != VerifyFail {
			return nil
		}
		ok = append(ok, c)
	}
	if len(ok) == 0 {
		return nil
	}
	var out []Observation
	for _, c := range ok {
		out = append(out, observe(in.Run, c, nil))
	}
	return out
}

// fireUnlistedFile is R11. A run that listed no files says nothing about scope,
// so it is skipped rather than read as "every file is out of scope".
func fireUnlistedFile(in ruleInput) []Observation {
	if len(in.Run.TaskFiles) == 0 {
		return nil
	}
	listed := map[string]bool{}
	for _, f := range in.Run.TaskFiles {
		listed[f] = true
	}
	var out []Observation
	for _, c := range in.Run.Candidates {
		var extra []string
		for _, f := range c.DiffFiles {
			if !listed[f] {
				extra = append(extra, f)
			}
		}
		if len(extra) == 0 {
			continue
		}
		out = append(out, observe(in.Run, c, map[string]string{
			"extra": strings.Join(sortedUnique(extra), ", "),
		}))
	}
	return out
}

// fireEditsTests is R12. The instruction has to be readable for the rule to
// fire: "the instruction does not ask for it" is a claim about the instruction,
// and a trace whose task directory is gone cannot support it.
func fireEditsTests(in ruleInput) []Observation {
	if in.Instruction == nil || in.Run.TaskDir == "" {
		return nil
	}
	text, ok := in.Instruction(in.Run.TaskDir)
	if !ok || asksForTests.MatchString(text) {
		return nil
	}
	var out []Observation
	for _, c := range in.Run.Candidates {
		var tests []string
		for _, f := range c.DiffFiles {
			if testFile.MatchString(f) {
				tests = append(tests, f)
			}
		}
		if len(tests) == 0 {
			continue
		}
		out = append(out, observe(in.Run, c, map[string]string{
			"file": strings.Join(sortedUnique(tests), ", "),
		}))
	}
	return out
}

// fireSingleProposer is R13: a selection nobody else could have made.
func fireSingleProposer(in ruleInput) []Observation {
	if in.Run.Selection.Kind != SelectionSelected || len(in.Run.Selection.AlsoPassed) > 0 {
		return nil
	}
	for _, c := range in.Run.Candidates {
		if c.ProposerID == in.Run.Selection.CandidateID {
			return []Observation{observe(in.Run, c, nil)}
		}
	}
	return nil
}

// fireBandBreach is R14. One invariant is one bucket: two invariants outside
// their bands are two failures, and an edit that moves one moves neither of the
// other's numbers.
func fireBandBreach(in ruleInput) []Observation {
	var out []Observation
	for _, c := range in.Run.Candidates {
		v, ok := in.Run.Verifies[c.ProposerID]
		if !ok {
			continue
		}
		rows := map[string]BandRow{}
		for _, row := range v.BandRows {
			rows[row.Invariant] = row
		}
		for _, invariant := range sortedUnique(v.BandFailed) {
			notes := map[string]string{}
			if row, found := rows[invariant]; found {
				notes["value"] = number(row.Value)
				notes["band_lo"] = number(row.BandLo)
				notes["band_hi"] = number(row.BandHi)
			}
			obs := observe(in.Run, c, notes)
			obs.Key = invariant
			out = append(out, obs)
		}
	}
	return out
}

// measurement renders R14's numbers, and says so plainly where the verifier
// reported none: an unmeasured invariant is not a measurement of zero.
func measurement(b Bucket) string {
	first := b.First()
	value, lo, hi := first.note("value"), first.note("band_lo"), first.note("band_hi")
	if value == "" || lo == "" || hi == "" {
		return "the measurement fell outside the band it is held to"
	}
	return fmt.Sprintf("value %s fell outside the band [%s,%s]", value, lo, hi)
}

// observe builds one observation from a run and the candidate it is about.
func observe(run *Run, c Candidate, notes map[string]string) Observation {
	model := c.Model
	if model == "" {
		model = run.Model(c.ProposerID)
	}
	if notes == nil {
		notes = map[string]string{}
	}
	pool := len(run.Proposers)
	if pool == 0 {
		pool = len(run.Candidates)
	}
	return Observation{
		RunID:    run.ID,
		Task:     run.TaskID,
		Proposer: c.ProposerID,
		Model:    model,
		Pool:     pool,
		Notes:    notes,
	}
}

// number renders a measurement, or the empty string where there was none.
func number(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

// sortedUnique is the one way this package turns a set of observed strings into
// a sentence: sorted, so two mining passes agree, and deduplicated, so a repeat
// is not a second fact.
func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// listMax is how many observed values a context sentence names before it stops
// counting them out. `context:` is a required STPA field a person reads; a
// bucket spanning forty runs would otherwise put forty comma-separated numbers
// on one YAML line and say nothing anybody could act on.
const listMax = 4

// capped shortens a list to something a reader can take in, saying how much it
// left out rather than trailing off.
func capped(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	out := append([]string(nil), values[:max]...)
	return append(out, fmt.Sprintf("and %d more", len(values)-max))
}

// list joins values for a sentence, and says "unknown" for none: a context that
// trails off mid-clause is worse than one that admits what it did not see.
func list(values []string) string {
	if len(values) == 0 {
		return "unknown"
	}
	return strings.Join(capped(values, listMax), ", ")
}

// span renders a set of observed numbers as the range they covered. A token
// ceiling hit at 4096, 4097 and 5000 is one fact about a ceiling, not three, and
// writing it as a range is what keeps the sentence readable however long the
// corpus gets.
func span(values []string) string {
	if len(values) == 0 {
		return "unknown"
	}
	low, high, numeric := 0, 0, true
	for i, v := range values {
		n, err := strconv.Atoi(v)
		if err != nil {
			numeric = false
			break
		}
		if i == 0 || n < low {
			low = n
		}
		if i == 0 || n > high {
			high = n
		}
	}
	if !numeric {
		return list(values)
	}
	if low == high {
		return strconv.Itoa(low)
	}
	return fmt.Sprintf("%d-%d", low, high)
}

// named prefixes a list with its noun, singular or plural as the list needs.
func named(noun string, values []string) string {
	switch len(values) {
	case 0:
		return "an unnamed " + noun
	case 1:
		return noun + " " + values[0]
	default:
		return noun + "s " + strings.Join(capped(values, listMax), ", ")
	}
}
