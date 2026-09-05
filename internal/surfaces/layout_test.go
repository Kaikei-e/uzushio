package surfaces_test

import (
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/surfaces"
)

// TestComponentForPath is the map that makes an edit's paths checkable against
// its component. It is a map of shapes rather than of prefixes on purpose: a
// nested memory note would sort somewhere a reader cannot predict from the
// listing, and a second file in a skill directory has no rendering at all.
func TestComponentForPath(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		path string
		want string
	}{
		{"system-prompt.md", "system-prompt"},
		{"memory/00-conventions.md", "memory"},
		{"memory/a.md", "memory"},
		{"skills/emit-diff/SKILL.md", "skill"},
		{"skills/a1/SKILL.md", "skill"},
	} {
		got, err := surfaces.ComponentForPath(c.path)
		if err != nil {
			t.Errorf("ComponentForPath(%q): %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("ComponentForPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	for _, path := range []string{
		"README.md",
		"memory/notes/deep.md",
		"memory/note.txt",
		"skills/emit-diff/README.md",
		"skills/emit-diff/nested/SKILL.md",
		"skills/Emit-Diff/SKILL.md",
		"agents/reviewer.md",
		"hooks.json",
		"/system-prompt.md",
		"../system-prompt.md",
		"",
	} {
		if got, err := surfaces.ComponentForPath(path); err == nil {
			t.Errorf("ComponentForPath(%q) = %q, want a refusal", path, got)
		}
	}
}

// TestInjectable: three of the seven surfaces are file-shaped in v0, and the
// other four are proposals nothing can render.
func TestInjectable(t *testing.T) {
	t.Parallel()
	all, err := surfaces.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	injectable := surfaces.Injectable()
	if len(injectable) != 3 {
		t.Errorf("Injectable() = %v, want three surfaces", injectable)
	}
	for _, name := range injectable {
		found := false
		for _, surface := range all {
			if surface == name {
				found = true
			}
		}
		if !found {
			t.Errorf("Injectable names %q, which is not a declared surface", name)
		}
		if !surfaces.HasInjectionPoint(name) {
			t.Errorf("HasInjectionPoint(%q) is false", name)
		}
	}
	for _, name := range []string{"tool-description", "tool-implementation", "middleware", "subagent-config"} {
		if surfaces.HasInjectionPoint(name) {
			t.Errorf("HasInjectionPoint(%q) is true; the rendered harness has no place for it", name)
		}
	}
}

// TestSkillDescription derives the line a skill contributes to the prompt, the
// way the harness derives it.
func TestSkillDescription(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{
			"from the frontmatter",
			"---\nname: emit-diff\ndescription: Emit exactly one unified diff.\n---\n\n# Emit a diff\n\nA body.\n",
			"Emit exactly one unified diff.",
		},
		{
			"a quoted description",
			"---\ndescription: \"Emit one diff.\"\n---\n\nA body.\n",
			"Emit one diff.",
		},
		{
			"from the first line that is not a heading",
			"# Read before edit\n\nReproduce context lines byte for byte.\n",
			"Reproduce context lines byte for byte.",
		},
		{
			"frontmatter without a description falls through to the body",
			"---\nname: a-skill\n---\n\n# A skill\n\nDo the thing.\n",
			"Do the thing.",
		},
	} {
		got, err := surfaces.SkillDescription([]byte(c.body))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: description = %q, want %q", c.name, got, c.want)
		}
	}
	for _, body := range []string{"", "# Only a heading\n", "\n\n## Another\n# And another\n"} {
		if _, err := surfaces.SkillDescription([]byte(body)); err == nil {
			t.Errorf("SkillDescription(%q) found a description", body)
		} else if !strings.Contains(err.Error(), "no description") {
			t.Errorf("error %q does not say the skill has no description", err)
		}
	}
}

// TestSkillNameIsBoundedAsTheHarnessBoundsIt is M10: a name this map accepts
// and the harness rejects turns an over-long skill into a run that cannot
// start, instead of an edit refused with a reason.
func TestSkillNameIsBoundedAsTheHarnessBoundsIt(t *testing.T) {
	t.Parallel()
	if surfaces.SkillNamePattern != `[a-z0-9][a-z0-9._-]{0,63}` {
		t.Errorf("SkillNamePattern = %q, want the harness's own bound", surfaces.SkillNamePattern)
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"a", true},
		{"emit-diff", true},
		{"read.before_edit-2", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"-leading-hyphen", false},
		{".leading-dot", false},
		{"Emit-Diff", false},
		{"has space", false},
	} {
		_, err := surfaces.ComponentForPath("skills/" + c.name + "/SKILL.md")
		if c.ok && err != nil {
			t.Errorf("skill name %q (%d chars) was refused: %v", c.name, len(c.name), err)
		}
		if !c.ok && err == nil {
			t.Errorf("skill name %q (%d chars) was accepted", c.name, len(c.name))
		}
	}
}
