package fixture_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Kaikei-e/DocDag/config"
	"github.com/Kaikei-e/DocDag/lint"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/fixture"
	"github.com/Kaikei-e/uzushio/internal/vault"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// fixtureDir is where the committed corpora live, relative to the repository
// root. Everything here reads them from there rather than regenerating into a
// temporary directory, because what has to hold is that the tree on disk is the
// one the generator would write.
const fixtureDir = "lint"

func TestCommittedFixturesAreGenerated(t *testing.T) {
	changed, err := fixture.Check(filepath.Join(repoRoot(t), fixtureDir))
	if err != nil {
		t.Fatalf("fixture.Check() error = %v", err)
	}
	if len(changed) > 0 {
		t.Errorf("fixture.Check() reports %d file(s) out of step with the generator: %v\n"+
			"run: go run ./internal/fixture/gen -out lint", len(changed), changed)
	}
}

func TestWriteIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	for round := range 2 {
		if err := fixture.Write(dir); err != nil {
			t.Fatalf("round %d: fixture.Write() error = %v", round, err)
		}
		changed, err := fixture.Check(dir)
		if err != nil {
			t.Fatalf("round %d: fixture.Check() error = %v", round, err)
		}
		if len(changed) > 0 {
			t.Fatalf("round %d: writing then checking reports %v", round, changed)
		}
	}
}

// TestFixtureLayer runs DocDag's fixture layer over the committed corpora: every
// ruleid corpus has to report its rule, every ok corpus has to stay silent, and
// every configured rule has to have both. Nothing here reads the vault.
func TestFixtureLayer(t *testing.T) {
	// lint.Check reroots the configuration's kind directories against the
	// process's working directory when no vault is named, so the relative
	// fixtures path has to be read from the repository root.
	t.Chdir(repoRoot(t))
	findings, err := lint.Check(uzushioConfig(t), "", fixtureDir)
	if err != nil {
		t.Fatalf("lint.Check() error = %v", err)
	}
	for _, f := range findings {
		if f.Severity == model.SeverityError || f.Severity == model.SeverityWarn {
			t.Errorf("lint.Check(cfg, \"\", %q) reported %s %s %s: %s", fixtureDir, f.Severity, f.Rule, f.ID, f.Detail)
		}
	}
}

// TestAllLayers is `docdag lint --all`: the configuration, the fixtures and the
// repository's own corpus.
//
// The info findings it leaves are facts about a young vault rather than faults,
// and they are these:
//
//   - unused_edge_in_corpus for deviates-from, measures, excepts, predicts and
//     validates — declared edges no document in spec/ has drawn yet. supersedes
//     is not among them: the premise lineage in spec/premises draws one;
//   - never_fired for the ten preset rules, softened to info by the ruleid
//     fixtures copied from DocDag;
//   - never_fired for uzushio's nine rules, reported as "0 of 0 edit documents:
//     the corpus holds none it could apply to" while spec/edits, spec/patterns
//     and spec/runs are empty.
func TestAllLayers(t *testing.T) {
	root := repoRoot(t)
	findings, err := lint.Check(uzushioConfig(t), root, filepath.Join(root, fixtureDir))
	if err != nil {
		t.Fatalf("lint.Check() error = %v", err)
	}
	for _, f := range findings {
		if f.Severity == model.SeverityError || f.Severity == model.SeverityWarn {
			t.Errorf("lint.Check(cfg, root, lint) reported %s %s %s: %s", f.Severity, f.Rule, f.ID, f.Detail)
		}
	}
}

// TestNamesCoverEveryRuleAndProjection asks the one question a fixture table
// cannot answer about itself: is anything uzushio declares left without a pair?
func TestNamesCoverEveryRuleAndProjection(t *testing.T) {
	names, err := fixture.Names()
	if err != nil {
		t.Fatalf("fixture.Names() error = %v", err)
	}
	want := append(vault.RuleNames(), vocab.Strings(vocab.AllProjections())...)
	// The binding projection and the lineage projection are the preset's;
	// uzushio adds one kind-scoped alternative to the first and its edits take
	// part in the supersedes edge the second reads, so both get a pair here.
	want = append(want, config.ProjectionEffective, config.ProjectionInForceSuccessor)
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("fixture.Names() does not cover %q", name)
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("fixture.Names() lists %q twice", name)
		}
		seen[name] = true
	}
}

func uzushioConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config() error = %v", err)
	}
	return cfg
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	return root
}
