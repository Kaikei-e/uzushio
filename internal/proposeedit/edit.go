package proposeedit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// DefaultTopic is the subject a proposed harness edit is about where the caller
// names none. An edit in force has to state a topic — two edits can only be
// seen to disagree where they are known to be about one thing.
const DefaultTopic = "topic/harness-improvement"

// RootCauseMax bounds the root cause a candidate's prose contributes. The field
// is frontmatter, and frontmatter that runs to a page is frontmatter nobody
// reads; the whole completion is in the trace for anyone who wants it.
const RootCauseMax = 600

// Proposal is one candidate turned into a vault document, or the reason it was
// not turned into one.
type Proposal struct {
	// Candidate is the answer this came from.
	Candidate Candidate
	// Edit is the document, valid and ready to write. Its EditID is empty until
	// Write assigns one.
	Edit doc.Edit
	// Diff is the sidecar unified diff a system-prompt edit carries, nil for
	// every other component.
	Diff []byte
}

// Refusal is a candidate that will not become an edit, with the reason.
type Refusal struct {
	// Candidate is the answer that was refused.
	Candidate Candidate
	// Reason says what was wrong with it, in one sentence.
	Reason string
}

// ConvertOptions are what turning a candidate into an edit needs beyond the
// candidate itself.
type ConvertOptions struct {
	// Harness is the rendered harness directory the candidate was proposed
	// against. The diff is applied to a copy of it, which is how the file
	// content a memory or skill edit carries is obtained.
	Harness string
	// Pattern is the failure the edit answers.
	Pattern doc.Pattern
	// Day is the day the edit is dated, as YYYY-MM-DD.
	Day string
	// Topic is the subject the edit is about. Empty means DefaultTopic.
	Topic string
	// Work is a directory the conversion may write scratch trees under.
	Work string
}

// Convert turns every candidate into a proposal, and says why about the ones it
// will not. A run is never all-or-nothing: one proposer answering badly is not a
// reason to throw away another's answer.
func Convert(ctx context.Context, candidates []Candidate, o ConvertOptions) ([]Proposal, []Refusal, error) {
	var proposals []Proposal
	var refusals []Refusal
	for i, candidate := range candidates {
		if candidate.Status != "ok" || len(candidate.Diff) == 0 {
			refusals = append(refusals, Refusal{
				Candidate: candidate,
				Reason:    fmt.Sprintf("the candidate's status is %q, so there is no diff to read", candidate.Status),
			})
			continue
		}
		work := filepath.Join(o.Work, fmt.Sprintf("apply-%02d-%s", i, candidate.ProposerID))
		proposal, reason, err := convertOne(ctx, candidate, o, work)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			refusals = append(refusals, Refusal{Candidate: candidate, Reason: reason})
			continue
		}
		proposals = append(proposals, proposal)
	}
	return proposals, refusals, nil
}

// convertOne applies one candidate's diff and reads the edit out of the result.
// A reason without an error is a refusal: the candidate was read and will not be
// written, which is an answer rather than a failure.
func convertOne(ctx context.Context, candidate Candidate, o ConvertOptions, work string) (Proposal, string, error) {
	repo := filepath.Join(work, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return Proposal{}, "", fmt.Errorf("%w: %w", ErrPropose, err)
	}
	if err := copyTree(o.Harness, repo); err != nil {
		return Proposal{}, "", err
	}
	if _, err := ensureSurfaces(repo); err != nil {
		return Proposal{}, "", err
	}
	if err := initRepo(ctx, repo); err != nil {
		return Proposal{}, "", err
	}
	diffPath := filepath.Join(work, "candidate.diff")
	if err := os.WriteFile(diffPath, candidate.Diff, 0o644); err != nil {
		return Proposal{}, "", fmt.Errorf("%w: %w", ErrPropose, err)
	}
	if _, err := git(ctx, repo, "apply", "--index", "--whitespace=nowarn", diffPath); err != nil {
		return Proposal{}, "the diff does not apply to the rendered harness", nil
	}
	changes, reason, err := changedPaths(ctx, repo)
	if err != nil {
		return Proposal{}, "", err
	}
	if reason != "" {
		return Proposal{}, reason, nil
	}
	component, reason := classify(changes)
	if reason != "" {
		return Proposal{}, reason, nil
	}
	edit, diff, reason, err := buildEdit(ctx, repo, component, changes, candidate, o)
	if err != nil {
		return Proposal{}, "", err
	}
	if reason != "" {
		return Proposal{}, reason, nil
	}
	return Proposal{Candidate: candidate, Edit: edit, Diff: diff}, "", nil
}

// changedPaths reads what the applied diff did, as paths and their statuses. A
// rename or a copy is refused rather than read: an edit owns files, and a file
// that moved is two facts about two paths that the vocabulary has no way to
// state as one.
func changedPaths(ctx context.Context, repo string) (map[string]string, string, error) {
	out, err := git(ctx, repo, "diff", "--cached", "--name-status", "-z", "HEAD")
	if err != nil {
		return nil, "", err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	changes := map[string]string{}
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" || path == "" {
			continue
		}
		if status[0] == 'R' || status[0] == 'C' {
			return nil, "the diff renames or copies a file, which an edit's paths cannot state", nil
		}
		// A deletion is unrepresentable, not unreadable. An edit states the
		// content of the file it owns and a deletion states none, so this is a
		// refusal — and it has to be one here rather than an unreadable file
		// three steps later, where it would abort every other proposer's answer
		// with it.
		if status[0] == 'D' {
			return nil, "the diff deletes " + path + "; an edit states the content of the file it " +
				"owns, and a deletion states none", nil
		}
		changes[path] = status
	}
	if len(changes) == 0 {
		return nil, "the diff applied and changed nothing", nil
	}
	return changes, "", nil
}

// classify says which surface a set of changed paths belongs to, and refuses a
// set that does not belong to exactly one.
//
// Two surfaces in one edit is refused rather than split. An edit is the unit a
// run measures and a supersession replaces, and one that changed two surfaces
// would make every verdict about it ambiguous: the vault could not say which
// half of it the measurement was about.
func classify(changes map[string]string) (string, string) {
	components := map[string][]string{}
	for path := range changes {
		component, err := surfaces.ComponentForPath(path)
		if err != nil {
			return "", fmt.Sprintf("the diff touches %s, which is not a path the rendered harness has "+
				"a place for", path)
		}
		components[component] = append(components[component], path)
	}
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 1 {
		return "", fmt.Sprintf("the diff touches %d surfaces (%s); an edit changes one",
			len(names), strings.Join(names, ", "))
	}
	return names[0], ""
}

// buildEdit assembles the document. The two halves of the hybrid split here: an
// additive surface stores the file, and the system-prompt surface stores a diff
// against whatever the rendered state was.
func buildEdit(
	ctx context.Context,
	repo, component string,
	changes map[string]string,
	candidate Candidate,
	o ConvertOptions,
) (doc.Edit, []byte, string, error) {
	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	topic := o.Topic
	if topic == "" {
		topic = DefaultTopic
	}
	approval, err := approvalFor(component)
	if err != nil {
		return doc.Edit{}, nil, "", err
	}
	edit := doc.Edit{
		// The identifier is assigned when the edit is written, because the next
		// free number is a fact about the vault and not about the candidate.
		EditID:    "he-0000",
		Title:     titleFor(component, paths, changes, o.Pattern.PatternID),
		Date:      o.Day,
		Status:    vocab.StatusProposed,
		Component: component,
		Touches:   []string{component},
		Paths:     paths,
		RootCause: rootCause(candidate.Raw, o.Pattern),
		Approval:  approval,
		About:     []string{topic},
		Predicts:  []doc.Prediction{{Pattern: o.Pattern.PatternID, Expect: vocab.ExpectFix}},
	}
	var sidecar []byte
	switch component {
	case "system-prompt":
		sidecar = candidate.Diff
		edit.DiffSHA256 = doc.DiffSHA256Of(sidecar)
		edit.Body = editBody(component, paths, o.Pattern)
	default:
		body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(paths[0])))
		if err != nil {
			return doc.Edit{}, nil, "", fmt.Errorf("%w: %w", ErrPropose, err)
		}
		text := strings.ReplaceAll(string(body), "\r\n", "\n")
		if strings.TrimSpace(text) == "" {
			return doc.Edit{}, nil, fmt.Sprintf("the diff leaves %s empty, so the edit would render "+
				"nothing", paths[0]), nil
		}
		// The document writer refuses a raw tab, because a tab is a corruption
		// that travels silently through YAML and through review. Rewriting it
		// here would change the file the proposer wrote into one nothing on
		// disk records — a memory edit keeps no sidecar diff, so the original
		// would be unrecoverable. Refuse it and say so.
		if strings.ContainsRune(text, '\t') {
			return doc.Edit{}, nil, fmt.Sprintf("the diff writes a tab into %s; a document's body "+
				"carries no raw tab, and rewriting it would lose what the proposer wrote", paths[0]), nil
		}
		edit.Body = strings.Trim(text, "\n")
	}
	if err := edit.CheckPaths(); err != nil {
		return doc.Edit{}, nil, fmt.Sprintf("the edit it would make is one a render refuses: %v", err), nil
	}
	return edit, sidecar, "", nil
}

// approvalFor reads who may accept an edit to a surface off CMoA's own
// declaration. uzushio does not decide it: the harness owns the surface, so it
// owns how much autonomy an edit to it gets.
func approvalFor(component string) (vocab.Approval, error) {
	auto, err := surfaces.ByAutonomy(surfaces.AutonomyAutoAccept)
	if err != nil {
		return "", err
	}
	if slices.Contains(auto, component) {
		return vocab.ApprovalAuto, nil
	}
	return vocab.ApprovalHuman, nil
}

// titleFor names the edit by what it does to which file, because that is what a
// reader scanning a directory of them needs to tell two apart.
func titleFor(component string, paths []string, changes map[string]string, pattern string) string {
	verb := "Change"
	switch {
	case component == "system-prompt":
		verb = "Amend"
	case len(paths) == 1 && strings.HasPrefix(changes[paths[0]], "A"):
		verb = "Add"
	case len(paths) == 1:
		verb = "Rewrite"
	}
	return fmt.Sprintf("%s %s to answer %s", verb, strings.Join(paths, ", "), pattern)
}

// editBody is what a system-prompt edit's body says. The document's body is the
// file content for the additive surfaces, so this is only ever reached for the
// one surface whose content lives in a sidecar diff, and it has to say where.
func editBody(component string, paths []string, pattern doc.Pattern) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Proposed by `uzushio improve --propose` against %s.\n\n", pattern.PatternID)
	fmt.Fprintf(&out, "The content of this edit is the sidecar unified diff beside it, applied to\n")
	fmt.Fprintf(&out, "`%s` in the rendered harness. The digest under `diff_sha256:` is what the\n",
		strings.Join(paths, "`, `"))
	out.WriteString("renderer checks the diff against before it applies it.\n\n")
	fmt.Fprintf(&out, "The failure it answers is %s: %s\n", pattern.PatternID, pattern.Context)
	return out.String()
}

// diffStart finds where a completion stops explaining and starts patching.
var diffStart = regexp.MustCompile(`(?m)^(diff --git |--- |\+\+\+ |@@ |` + "```" + `)`)

// rootCauseHeading is the heading the instruction asks for. Where it is there,
// the paragraph under it is the answer; where it is not, the prose before the
// diff is the best there is.
var rootCauseHeading = regexp.MustCompile(`(?i)root cause\s*[:\-]?\s*`)

// rootCause reads the diagnosis out of the completion's prose, falling back to
// the pattern's own context.
//
// The fallback matters more than it looks. An edit whose root_cause restates the
// pattern is an edit that explained nothing, and a reader can see that at a
// glance — which is the honest outcome when the proposer wrote nothing but a
// diff. Inventing a cause here would hide it.
func rootCause(raw string, pattern doc.Pattern) string {
	prose := raw
	if at := diffStart.FindStringIndex(prose); at != nil {
		prose = prose[:at[0]]
	}
	if at := rootCauseHeading.FindStringIndex(prose); at != nil {
		prose = prose[at[1]:]
	}
	prose = tidy(prose)
	if prose == "" {
		return tidy(pattern.Context)
	}
	if len(prose) > RootCauseMax {
		// Cut at a word boundary so the field does not end mid-word, and say
		// that it was cut: the whole completion is in the trace.
		cut := strings.LastIndex(prose[:RootCauseMax], " ")
		if cut < RootCauseMax/2 {
			cut = RootCauseMax
		}
		prose = strings.TrimSpace(prose[:cut]) + " […]"
	}
	return prose
}

// tidy folds prose into the one line a frontmatter scalar can hold. A tab or a
// carriage return in a document is a corruption that travels silently, and the
// document writer refuses one outright, so they are removed here rather than
// discovered three steps later.
func tidy(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
