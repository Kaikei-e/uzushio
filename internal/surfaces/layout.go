package surfaces

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The rendered harness directory is a tree of plain files, and every path in
// it belongs to exactly one surface. The map is the contract that makes an
// edit's `paths:` checkable against its `component:` and its `touches:`, which
// is the one thing DocDag cannot do for itself: it has no path arithmetic, so
// a rule there would have to compare strings it cannot take apart.
//
// Three of the seven surfaces are file-shaped in v0. The other four —
// tool-description, tool-implementation, middleware and subagent-config — name
// real surfaces of the harness but have no injection point in the rendered
// directory, so an edit about one of them is a proposal nobody can render, and
// `harness render` and `run` say so rather than rendering nothing quietly.
const (
	// SystemPromptFile is the whole of the system-prompt surface: one file at
	// the root, appended verbatim after the harness's own system template.
	SystemPromptFile = "system-prompt.md"
	// MemoryDir holds the memory surface, one note per file, rendered in
	// sorted order.
	MemoryDir = "memory"
	// SkillsDir holds the skill surface, one directory per skill.
	SkillsDir = "skills"
	// SkillFile is the file a skill directory must carry.
	SkillFile = "SKILL.md"
)

// skillPath is `skills/<name>/SKILL.md` with the name bounded exactly as the
// harness bounds it, so a skill directory cannot be nested and cannot carry a
// second file that would render as a second skill.
//
// The bound is not decoration. A name longer than the harness accepts renders
// fine here and makes the harness reject the whole directory, which turns an
// over-long skill name into a run that cannot start rather than into an edit
// that is refused with a reason.
var skillPath = regexp.MustCompile(`^` + SkillsDir + `/` + SkillNamePattern + `/` + SkillFile + `$`)

// SkillNamePattern is the shape a skill directory's name has, unanchored. It
// is the harness's own bound, repeated here because this is where an edit's
// paths are checked and nothing else in the corpus checks them.
const SkillNamePattern = `[a-z0-9][a-z0-9._-]{0,63}`

// memoryPath is `memory/<file>.md`, flat: a nested note would sort in a place
// a reader cannot predict from the listing.
var memoryPath = regexp.MustCompile(`^` + MemoryDir + `/[A-Za-z0-9][A-Za-z0-9._-]*\.md$`)

// Injectable returns the surfaces the rendered harness directory has a place
// for, in the order the renderer writes them.
func Injectable() []string {
	return []string{"system-prompt", MemoryDir, "skill"}
}

// HasInjectionPoint reports whether an edit about this component can be
// rendered at all.
func HasInjectionPoint(component string) bool {
	return slices.Contains(Injectable(), component)
}

// ComponentForPath returns the surface a path under the rendered harness
// directory belongs to. The path is the one the edit writes: relative, slash
// separated, no leading slash and no `..` segment.
func ComponentForPath(p string) (string, error) {
	if err := CheckHarnessPath(p); err != nil {
		return "", err
	}
	switch {
	case p == SystemPromptFile:
		return "system-prompt", nil
	case memoryPath.MatchString(p):
		return MemoryDir, nil
	case skillPath.MatchString(p):
		return "skill", nil
	}
	return "", fmt.Errorf(
		"%w: %q is not a path the rendered harness has a place for (want %s, %s/<name>.md or %s/<name>/%s)",
		ErrInvalid, p, SystemPromptFile, MemoryDir, SkillsDir, SkillFile)
}

// CheckHarnessPath reports a path that could not name a file inside the
// rendered harness directory whatever surface it claimed: absolute, escaping,
// unclean, or empty. It is separate from ComponentForPath so a caller that
// only wants to know the path is safe to join does not have to know the map.
func CheckHarnessPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: an edit path is empty", ErrInvalid)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: edit path %q is absolute", ErrInvalid, p)
	case strings.ContainsRune(p, '\\'):
		return fmt.Errorf("%w: edit path %q holds a backslash; harness paths are slash separated", ErrInvalid, p)
	case path.Clean(p) != p:
		return fmt.Errorf("%w: edit path %q is not clean (want %q)", ErrInvalid, p, path.Clean(p))
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." || segment == "." || segment == "" {
			return fmt.Errorf("%w: edit path %q holds a %q segment", ErrInvalid, p, segment)
		}
	}
	return nil
}

// SkillDescription derives the one line a skill contributes to the harness
// prompt, the way the harness derives it: the frontmatter `description:` where
// there is one, and otherwise the first non-empty line that is not a Markdown
// heading. Skill bodies are not rendered — the description is the whole of
// what a skill says until something invokes it — so a skill with no derivable
// description is a file that would reach the prompt as a name and nothing
// else, and the harness refuses it rather than listing a blank.
func SkillDescription(body []byte) (string, error) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	rest := text
	const fence = "---\n"
	if strings.HasPrefix(text, fence) {
		if end := strings.Index(text[len(fence):], "\n"+fence); end >= 0 {
			front := text[len(fence) : len(fence)+end+1]
			rest = text[len(fence)+end+len("\n")+len(fence):]
			for _, line := range strings.Split(front, "\n") {
				value, found := strings.CutPrefix(line, "description:")
				if !found {
					continue
				}
				if description := unquote(strings.TrimSpace(value)); description != "" {
					return description, nil
				}
			}
		}
	}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf(
		"%w: the skill has no description: it writes no `description:` in its frontmatter and its body has no line that is not a heading",
		ErrInvalid)
}

// unquote strips the quotes a YAML scalar may carry. It is not a YAML parser
// and does not need to be: a description is one line of prose, and the two
// things that happen to it in practice are being quoted and not being quoted.
func unquote(value string) string {
	for _, quote := range []string{`"`, `'`} {
		if len(value) >= 2 && strings.HasPrefix(value, quote) && strings.HasSuffix(value, quote) {
			return value[1 : len(value)-1]
		}
	}
	return value
}
