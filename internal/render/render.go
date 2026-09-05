// Package render materialises the harness the vault describes.
//
// The vault is the source and the directory is derived. What is in force on a
// given day is DocDag's answer, not uzushio's — `docdag query --binding` is
// asked rather than reimplemented, because one definition of "binding" is the
// point of having a vault at all — and this package turns that answer into a
// tree of files a harness can read.
//
// Two of the three surfaces the tree has a place for carry their content in
// the edit document's own body: a memory note and a skill are additive, one
// file each, and a change to one is a new edit that supersedes the old, so the
// document *is* the file and there is nothing to apply. The third does not
// work that way — an edit to the system prompt modifies text a previous edit
// wrote — so it carries a sidecar unified diff beside the document, and the
// diffs are applied in ascending edit id order onto an empty seed. Ascending
// id is the order key and the only one: it is what `docdag query --binding`
// already emits, it is zero-padded so lexical and numeric order agree, and it
// is the one key in the corpus no clock, timezone or rebase can move.
//
// Rendering is deterministic and it is hashed. Every walk is sorted on raw
// bytes, no wall clock reaches the manifest, and `render.json` carries the
// digest of every file and a digest over the whole tree, so "the same harness"
// is a decidable claim rather than an assumption — which is what a paired
// comparison between a baseline and a candidate rests on.
package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ErrRender is the sentinel every render failure wraps.
var ErrRender = errors.New("render")

// SchemaVersion is the version of the render.json this package writes.
const SchemaVersion = 1

// Version is the renderer's own version, recorded in every manifest. A view
// hash is meaningless without the projector version that produced it: the same
// vault rendered by two renderers is two trees, and a comparison that did not
// say which one it read is a comparison of nothing.
func Version() string { return "uzushio-render/1" }

// ManifestName is the file the manifest is written to inside the output
// directory. It is not part of the rendered tree and is not hashed into it.
const ManifestName = "render.json"

// Options is one render.
type Options struct {
	// Vault is the vault root, the directory docdag.yaml sits in.
	Vault string
	// AsOf is the day the harness is read for, as YYYY-MM-DD. Empty is today
	// in UTC.
	AsOf string
	// WithEdits are edits to add to the binding set: the candidate under
	// test. An edit that is already binding is an error, because "with" would
	// then be a no-op the caller believed did something.
	WithEdits []string
	// WithoutEdits are binding edits to remove: an ablation. An edit that is
	// not binding is an error, for the same reason.
	WithoutEdits []string
	// Out is the directory the tree is written to. It is created; an existing
	// non-empty directory is refused unless Force says otherwise.
	Out string
	// Force allows Out to be overwritten.
	Force bool
	// DocDag is the docdag binary. Empty means "docdag" on PATH.
	DocDag string
	// Now is the clock, and it is used for one thing only: the default AsOf.
	// Nothing a clock returns reaches the manifest.
	Now func() time.Time
}

// EditRecord is one edit in the render, as the manifest names it.
type EditRecord struct {
	ID string `json:"id"`
	// Component is the surface the edit is about, which is also the way its
	// content reached the tree.
	Component string `json:"component"`
	Status    string `json:"status"`
	// Paths are the files the edit owns.
	Paths []string `json:"paths"`
	// SHA256 is the digest of the edit's content: the body it wrote for a
	// memory or a skill, the sidecar diff for a system-prompt edit.
	SHA256 string `json:"sha256"`
	// Binding says the edit was in the vault's binding set rather than added
	// by --with-edit. It is what tells a failure to apply apart: a binding
	// edit that will not apply means the vault and the seed have diverged and
	// a person has to reconcile them, while a candidate that will not apply
	// is a measurement that cannot be taken.
	Binding bool `json:"binding"`
}

// FileRecord is one file in the rendered tree.
type FileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Manifest is what the render says about itself, written as render.json.
//
// It carries no timestamp. A render is a projection of a vault at a day and a
// revision, and those two are in the manifest; the wall clock at which the
// projection happened is not part of what was projected, and putting it in
// would make two renders of the same state two different files.
type Manifest struct {
	SchemaVersion int `json:"schema_version"`
	// AsOf is the day the binding set was read for.
	AsOf string `json:"as_of"`
	// At is the vault revision the documents were read from, with a `-dirty`
	// suffix where the working tree had changes. A render from a dirty vault
	// is not reconstructible and the suffix is how a reader finds out.
	At string `json:"at"`
	// DocDagVersion is what the engine that answered "binding" calls itself.
	DocDagVersion string `json:"docdag_version"`
	// Edits are the edits that reached the tree, in ascending id order, which
	// is the order they were applied in.
	Edits []EditRecord `json:"edits"`
	// SeedSHA256 is the tree digest of the seed the edits were applied to.
	// v0's seed is the empty tree, and the field is written anyway: the seed
	// is the only input that does not come from the vault, so a render that
	// did not name it would not be reproducible.
	SeedSHA256 string `json:"seed_sha256"`
	// TreeSHA256 is the digest of the whole rendered tree: SHA-256 over the
	// concatenation of "<path>\n<sha256>\n" for every file, sorted by path.
	// It is a manifest hash rather than a git tree object, so it survives
	// git's hash transition and can be recomputed without git.
	TreeSHA256 string `json:"tree_sha256"`
	// Files is every file in the tree, sorted by path.
	Files []FileRecord `json:"files"`
	// RendererVersion is Version().
	RendererVersion string `json:"renderer_version"`
	// OrderKey names how the edits were ordered, so a future change to the
	// order is visible in the record rather than silent.
	OrderKey string `json:"order_key"`
}

// orderKey is the one order the edits are applied in.
const orderKey = "edit_id_asc"

// ApplyError is a sidecar diff that would not apply. It is its own type
// because the caller answers it differently depending on which edit failed:
// see EditRecord.Binding.
type ApplyError struct {
	EditID  string
	DiffAt  string
	Binding bool
	Stderr  string
}

func (e *ApplyError) Error() string {
	which := "candidate"
	if e.Binding {
		which = "binding"
	}
	return fmt.Sprintf("render: the %s edit %s does not apply to the harness rendered before it (%s): %s",
		which, e.EditID, e.DiffAt, e.Stderr)
}

// Unwrap makes errors.Is(err, ErrRender) hold.
func (e *ApplyError) Unwrap() error { return ErrRender }

// Render writes the harness and returns the manifest describing it.
func Render(ctx context.Context, o Options) (Manifest, error) {
	if strings.TrimSpace(o.Vault) == "" {
		return Manifest{}, fmt.Errorf("%w: no vault", ErrRender)
	}
	if strings.TrimSpace(o.Out) == "" {
		return Manifest{}, fmt.Errorf("%w: no output directory", ErrRender)
	}
	asOf := o.AsOf
	if asOf == "" {
		now := time.Now
		if o.Now != nil {
			now = o.Now
		}
		asOf = now().UTC().Format(vocab.DayLayout)
	}
	if _, err := time.Parse(vocab.DayLayout, asOf); err != nil {
		return Manifest{}, fmt.Errorf("%w: --as-of %q is not a %s day", ErrRender, asOf, vocab.DayLayout)
	}
	if err := checkOut(o.Out, o.Force); err != nil {
		return Manifest{}, err
	}

	edits, err := plan(ctx, o, asOf)
	if err != nil {
		return Manifest{}, err
	}

	// The tree is built in a scratch directory that is also a git repository,
	// because applying a unified diff to a tree is what git is for and
	// reimplementing it would be a second patch semantics beside the one the
	// rest of the toolchain uses. It is moved into place only once it is
	// whole, so a failed render leaves no half-written harness behind.
	work, err := os.MkdirTemp("", "uzushio-render-")
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	tree := filepath.Join(work, "harness")

	records, err := build(ctx, o.Vault, tree, edits)
	if err != nil {
		return Manifest{}, err
	}
	files, err := Files(tree)
	if err != nil {
		return Manifest{}, err
	}
	seed, err := seedDigest()
	if err != nil {
		return Manifest{}, err
	}
	version, err := docdagVersion(ctx, o.DocDag)
	if err != nil {
		return Manifest{}, err
	}
	at, err := revision(ctx, o.Vault)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		SchemaVersion:   SchemaVersion,
		AsOf:            asOf,
		At:              at,
		DocDagVersion:   version,
		Edits:           records,
		SeedSHA256:      seed,
		TreeSHA256:      TreeDigest(files),
		Files:           files,
		RendererVersion: Version(),
		OrderKey:        orderKey,
	}
	body, err := Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeReadable(filepath.Join(tree, ManifestName), body); err != nil {
		return Manifest{}, err
	}
	if err := install(tree, o.Out); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Marshal renders a manifest the way it is written to disk: two-space indent,
// one trailing newline, no HTML escaping.
func Marshal(m Manifest) ([]byte, error) {
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("%w: encode the manifest: %w", ErrRender, err)
	}
	return []byte(out.String()), nil
}

// ReadManifest reads a render.json back.
func ReadManifest(pathname string) (Manifest, error) {
	body, err := os.ReadFile(pathname) //nolint:gosec // the caller names the manifest
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s: %w", ErrRender, pathname, err)
	}
	if m.SchemaVersion != SchemaVersion {
		return Manifest{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrRender, pathname, m.SchemaVersion, SchemaVersion)
	}
	return m, nil
}

// TreeDigest is the definition of tree_sha256: SHA-256 over the concatenation
// of "<path>\n<sha256>\n" for every file, sorted by path on raw bytes.
//
// It is a manifest hash rather than git's tree object, deliberately. It
// survives git's SHA-1 to SHA-256 transition, it can be recomputed by anything
// that can read a directory and hash a file, and it is the same number on
// every filesystem because nothing about the file except its path and its
// content goes into it.
func TreeDigest(files []FileRecord) string {
	sorted := slices.Clone(files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	sum := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(sum, "%s\n%s\n", f.Path, f.SHA256)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Digest returns the tree digest of a rendered harness directory on disk.
func Digest(root string) (string, error) {
	files, err := Files(root)
	if err != nil {
		return "", err
	}
	return TreeDigest(files), nil
}

// seedDigest is the tree digest of the seed. v0's seed is the empty tree.
func seedDigest() (string, error) { return TreeDigest(nil), nil }

// checkOut refuses to write over a directory that already holds something,
// unless the caller said to. A render that silently merged into whatever was
// there would produce a tree no manifest describes.
//
// It is a predicate and nothing else. Removing the old directory here would
// defeat the whole build-in-a-temp-directory-then-install design: a render
// that failed at the apply step would already have destroyed the harness that
// was working, and `--force` would mean "delete this whether or not I can
// replace it". The removal happens in install, once the new tree is whole.
func checkOut(out string, force bool) error {
	entries, err := os.ReadDir(out)
	switch {
	case errors.Is(err, fs.ErrNotExist), force:
		return nil
	case err != nil:
		return fmt.Errorf("%w: %w", ErrRender, err)
	case len(entries) == 0:
		return nil
	}
	return fmt.Errorf("%w: %s is not empty; pass --force to overwrite it", ErrRender, out)
}

// install moves the finished tree into place.
func install(tree, out string) error {
	if err := makeDir(filepath.Dir(filepath.Clean(out))); err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	if err := os.Rename(tree, out); err == nil {
		return nil
	}
	// A rename across filesystems fails, and the temporary directory is
	// wherever TMPDIR points, which need not be the same filesystem as the
	// output. Copying is the fallback rather than the rule because a rename
	// is atomic and a copy is not.
	if err := copyTree(tree, out); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	return nil
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, name)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return makeDir(target)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrRender, relative)
		}
		body, err := os.ReadFile(name) //nolint:gosec // a file this process just wrote
		if err != nil {
			return err
		}
		return writeReadable(target, body)
	})
}

// writeReadable writes a file and then makes it readable, because the mode
// passed to WriteFile is masked by the process umask and the harness is read
// by a container that may run as another uid. Under `umask 077` the file would
// otherwise be 0600, and the render would be a harness nobody can read. The
// tree digest carries no mode, so this changes no hash.
func writeReadable(name string, body []byte) error {
	if err := os.WriteFile(name, body, 0o644); err != nil { //nolint:gosec // a rendered harness is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	return nil
}

// makeDir creates a directory and makes it traversable, for the same reason.
func makeDir(name string) error {
	if err := os.MkdirAll(name, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	return nil
}

// Files walks a rendered harness directory and returns one record per file,
// sorted by path. It is exported because it is how a caller checks that the
// directory another process read is the directory this one wrote: the harness
// computes the same list from the directory it was handed, and the two digests
// either agree or the comparison the run is about is not a comparison.
//
// The walk is sorted by filepath.WalkDir's own lexical order and the result is
// sorted again on the slash-separated path, because those two differ once a
// directory name is a prefix of another.
func Files(root string) ([]FileRecord, error) {
	var files []FileRecord
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		// Two things in the directory are not part of the harness and are not
		// hashed into it: a git directory, which is scaffolding for applying
		// the diffs, and the manifest, which is a statement *about* the tree
		// and cannot be inside its own digest. The harness that reads the
		// directory excludes exactly these two, and it has to be exactly
		// these two or the two digests never agree.
		if entry.IsDir() {
			if relative == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == ManifestName {
			return nil
		}
		// Reading a file dereferences a symlink, so a link in the tree would
		// be hashed as whatever it points at — the digest would stop being a
		// fact about the vault. Nothing but a regular file belongs here.
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrRender, relative)
		}
		body, err := os.ReadFile(name) //nolint:gosec // a file this process just wrote
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		files = append(files, FileRecord{Path: relative, SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRender, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// planned is one edit on its way into the tree.
type planned struct {
	edit    doc.Edit
	binding bool
	diff    []byte
}

// plan works out which edits reach the tree and in what order, and reads each
// one's document. The binding set is DocDag's answer; --with-edit adds a
// candidate that is not in it and --without-edit takes one out.
func plan(ctx context.Context, o Options, asOf string) ([]planned, error) {
	binding, err := bindingEdits(ctx, o.DocDag, o.Vault, asOf)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, id := range binding {
		ids[id] = true
	}
	for _, id := range o.WithoutEdits {
		if !ids[id] {
			return nil, fmt.Errorf("%w: --without-edit %s is not binding as of %s, so removing it would change nothing",
				ErrRender, id, asOf)
		}
		delete(ids, id)
	}
	added := map[string]bool{}
	for _, id := range o.WithEdits {
		if ids[id] {
			return nil, fmt.Errorf("%w: --with-edit %s is already binding as of %s; render it without the flag or ablate it with --without-edit",
				ErrRender, id, asOf)
		}
		if !vocab.ValidEditID(id) {
			return nil, fmt.Errorf("%w: --with-edit %q is not an edit identifier (want %s)", ErrRender, id, vocab.EditIDPattern)
		}
		ids[id] = true
		added[id] = true
	}

	ordered := maps(ids)
	slices.Sort(ordered)
	out := make([]planned, 0, len(ordered))
	owned := map[string]string{}
	for _, id := range ordered {
		edit, err := readEdit(o.Vault, id)
		if err != nil {
			return nil, err
		}
		if err := edit.CheckPaths(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRender, err)
		}
		p := planned{edit: edit, binding: !added[id]}
		if edit.Component == componentSystemPrompt {
			p.diff, err = readDiff(o.Vault, edit)
			if err != nil {
				return nil, err
			}
		} else {
			// Two live edits owning one file is the collision the vault
			// cannot see: supersession is how a memory note is changed, and
			// two edits that both claim a path are a state a person has to
			// resolve rather than a state the renderer may pick a winner in.
			for _, name := range edit.Paths {
				if other, taken := owned[name]; taken {
					return nil, fmt.Errorf("%w: edits %s and %s both own %s; one supersedes the other or one of them is wrong",
						ErrRender, other, id, name)
				}
				owned[name] = id
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// componentSystemPrompt is the one surface whose content is a sidecar diff
// rather than the edit document's own body.
const componentSystemPrompt = "system-prompt"

// maps returns a set's keys as a sequence, so the caller can sort them. It is
// spelled out rather than pulled from the standard library's maps package
// because that name is taken by the import above in every other file here.
func maps(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// readEdit reads and parses one edit document out of the vault.
func readEdit(vault, id string) (doc.Edit, error) {
	relative, err := vocab.Path(vocab.KindEdit, id)
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	edit, err := doc.ParseEdit(id, body)
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	return edit, nil
}

// readDiff reads a system-prompt edit's sidecar and checks it against the
// digest the document declares. The digest is the whole protection the sidecar
// has: DocDag parses only .md and its append-only check skips everything else,
// so the bytes of a .diff are invisible to the vault, and the one thing that
// makes rewriting them visible is that the frontmatter has to change too.
func readDiff(vault string, edit doc.Edit) ([]byte, error) {
	relative, err := doc.DiffPath(edit.EditID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRender, err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		return nil, fmt.Errorf("%w: edit %s declares diff_sha256 but its sidecar is unreadable: %w",
			ErrRender, edit.EditID, err)
	}
	if got := doc.DiffSHA256Of(body); got != edit.DiffSHA256 {
		return nil, fmt.Errorf("%w: %s has digest %s but edit %s declares %s; the sidecar has been rewritten",
			ErrRender, relative, got, edit.EditID, edit.DiffSHA256)
	}
	return body, nil
}

// build writes the tree: the empty seed, then every edit in ascending id
// order, then the two directories the contract says are always there.
func build(ctx context.Context, vault, tree string, edits []planned) ([]EditRecord, error) {
	if err := makeDir(tree); err != nil {
		return nil, err
	}
	if err := git(ctx, tree, "init", "--quiet"); err != nil {
		return nil, err
	}
	records := make([]EditRecord, 0, len(edits))
	for _, p := range edits {
		record := EditRecord{
			ID:        p.edit.EditID,
			Component: p.edit.Component,
			Status:    p.edit.Status.String(),
			Paths:     p.edit.Paths,
			Binding:   p.binding,
		}
		if p.diff != nil {
			record.SHA256 = doc.DiffSHA256Of(p.diff)
			if err := apply(ctx, tree, p); err != nil {
				return nil, err
			}
		} else {
			content, err := p.edit.Content()
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrRender, err)
			}
			// A skill reaches the prompt as one line: its name and its
			// description. A SKILL.md with neither a `description:` nor a
			// body line that is not a heading would reach it as a name and a
			// blank, and the harness that reads the directory refuses the
			// whole render for it — so the edit is refused here, where the
			// message can name the edit that wrote the file.
			if p.edit.Component == "skill" {
				if _, err := surfaces.SkillDescription(content); err != nil {
					return nil, fmt.Errorf("%w: edit %s writes %s: %w",
						ErrRender, p.edit.EditID, p.edit.Paths[0], err)
				}
			}
			sum := sha256.Sum256(content)
			record.SHA256 = hex.EncodeToString(sum[:])
			target := filepath.Join(tree, filepath.FromSlash(p.edit.Paths[0]))
			if err := makeDir(filepath.Dir(target)); err != nil {
				return nil, err
			}
			if err := writeReadable(target, content); err != nil {
				return nil, err
			}
		}
		records = append(records, record)
	}
	// The empty directories are part of the contract, not an accident of
	// which edits happen to be in force: they are what tells a proposer where
	// it is allowed to act. git cannot carry an empty directory and neither
	// can a file listing, so each holds a zero-byte .gitkeep.
	for _, dir := range []string{surfaces.MemoryDir, surfaces.SkillsDir} {
		if err := makeDir(filepath.Join(tree, dir)); err != nil {
			return nil, err
		}
		if err := writeReadable(filepath.Join(tree, dir, ".gitkeep"), nil); err != nil {
			return nil, err
		}
	}
	if err := os.RemoveAll(filepath.Join(tree, ".git")); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRender, err)
	}
	return records, nil
}

// apply puts one sidecar diff into the tree, strictly.
//
// No --recount, no --ignore-whitespace, no fuzz and no context reduction. A
// lenient apply is right for a small model's guess at a patch, which is what
// CMoA does with a candidate, and wrong for a vault record: leniency relaxes
// context lines, so "it applied" and "it applied where it meant to" come
// apart, and the rendered harness has no verifier behind it — only a hash.
func apply(ctx context.Context, tree string, p planned) error {
	name := filepath.Join(tree, ".uzushio-render.diff")
	if err := os.WriteFile(name, p.diff, 0o600); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	defer func() { _ = os.Remove(name) }()

	// What the diff *does* is checked before it is allowed to do it. The
	// document's `paths:` is a claim by the edit's author; `--numstat
	// --summary --check` is git's account of the same patch, and the two have
	// to agree. Three things are refused here that a clean apply would
	// otherwise let through:
	//
	//   - a mode of 120000 or 160000 — a symlink or a submodule. The tree is
	//     hashed by reading each file, which dereferences a symlink, so a
	//     planted link makes tree_sha256 a function of the host rather than of
	//     the vault, and the copy path materialises the target's bytes into
	//     the harness the fleet is given.
	//   - a binary patch, which numstat reports as "-\t-". The harness reads
	//     Markdown; a binary blob in it is not content, it is payload.
	//   - a path the edit does not own. A system-prompt edit that writes a
	//     skill launders the approval rule: system-prompt is human-approval
	//     and skill is auto-accept, so a reviewed system-prompt change could
	//     carry skills nobody reviewed as skills.
	//
	// These diffs are model-generated, so none of this is hypothetical.
	if err := inspect(ctx, tree, name, p); err != nil {
		return err
	}
	out, err := gitOutput(ctx, tree, "apply", "--index", "--whitespace=nowarn", name)
	if err != nil {
		relative, _ := doc.DiffPath(p.edit.EditID)
		return &ApplyError{
			EditID:  p.edit.EditID,
			DiffAt:  relative,
			Binding: p.binding,
			Stderr:  strings.TrimSpace(out),
		}
	}
	return nil
}

// modeLine matches the `mode` lines `git apply --summary` writes.
var modeLine = regexp.MustCompile(`^(create|delete) mode ([0-7]{6}) (.+)$`)

// inspect enumerates a patch and holds it to what the edit says it does.
func inspect(ctx context.Context, tree, name string, p planned) error {
	refuse := func(format string, a ...any) error {
		relative, _ := doc.DiffPath(p.edit.EditID)
		return fmt.Errorf("%w: %s (%s): %s", ErrRender, p.edit.EditID, relative, fmt.Sprintf(format, a...))
	}
	out, err := gitOutput(ctx, tree, "apply", "--numstat", "--summary", "--check", name)
	if err != nil {
		relative, _ := doc.DiffPath(p.edit.EditID)
		return &ApplyError{
			EditID:  p.edit.EditID,
			DiffAt:  relative,
			Binding: p.binding,
			Stderr:  strings.TrimSpace(out),
		}
	}
	touched := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		// A summary line is indented by one space and a numstat row is not,
		// so trimming the left is safe and is what lets one loop read both.
		line = strings.TrimLeft(strings.TrimRight(line, "\r"), " ")
		if line == "" {
			continue
		}
		if match := modeLine.FindStringSubmatch(line); match != nil {
			if err := checkMode(refuse, match[2], match[3]); err != nil {
				return err
			}
			continue
		}
		if rest, found := strings.CutPrefix(line, "mode change "); found {
			for _, mode := range strings.Fields(rest) {
				if len(mode) == 6 && strings.HasPrefix(mode, "1") && mode != "100644" && mode != "100755" {
					return refuse("changes a file to mode %s; the harness holds regular files only", mode)
				}
			}
			continue
		}
		// A numstat row is "<added>\t<deleted>\t<path>", and a binary patch
		// writes "-" for both counts.
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		if fields[0] == "-" && fields[1] == "-" {
			return refuse("carries a binary patch for %s; the harness holds text", fields[2])
		}
		touched[unquotePath(fields[2])] = true
	}
	for name := range touched {
		component, err := surfaces.ComponentForPath(name)
		if err != nil {
			return refuse("writes %s: %v", name, err)
		}
		if component != p.edit.Component {
			return refuse("is about %q but writes %s, which is %q",
				p.edit.Component, name, component)
		}
		if !slices.Contains(p.edit.Paths, name) {
			return refuse("writes %s, which is not one of the files it says it owns (%v)",
				name, p.edit.Paths)
		}
	}
	return nil
}

// checkMode refuses the file modes a harness has no reading for.
//
// Only 100644. The renderer writes every file 0644 and the tree digest does
// not carry a mode at all, so a patch asking for anything else is asking for
// something the render cannot honour and the manifest cannot record — better
// refused than silently flattened.
func checkMode(refuse func(string, ...any) error, mode, name string) error {
	switch mode {
	case "100644":
		return nil
	case "120000":
		return refuse("creates or deletes the symlink %s; the harness holds regular files only, "+
			"and hashing a link would make the tree digest a fact about the machine", name)
	}
	return refuse("gives %s mode %s; the harness holds ordinary readable files, written 0644", name, mode)
}

// unquotePath undoes the C-style quoting git applies to a path with unusual
// bytes in it. The harness's path shapes admit none of them, so the only thing
// this has to do is hand the quoted form on unchanged — the surface map then
// refuses it by name rather than by a confusing parse error.
func unquotePath(name string) string {
	if unquoted, err := strconv.Unquote(name); err == nil {
		return unquoted
	}
	return name
}

// bindingEdits asks DocDag which documents are binding on a day and keeps the
// edits. There is no --kind flag, so the filter is the kind's directory, which
// is the same thing said in the vocabulary the configuration was generated
// from.
func bindingEdits(ctx context.Context, docdag, vault, asOf string) ([]string, error) {
	type row struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	out, err := docdagOutput(ctx, docdag, vault,
		"query", "--binding", "--fields", "id,path", "--format", "json", "--as-of", asOf)
	if err != nil {
		return nil, err
	}
	var rows []row
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("%w: docdag query returned something that is not the JSON this reads: %w", ErrRender, err)
	}
	var ids []string
	for _, r := range rows {
		if path.Dir(r.Path) != vocab.DirEdits {
			continue
		}
		if !vocab.ValidEditID(r.ID) {
			continue
		}
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids, nil
}

// docdagVersion records what answered the binding question.
func docdagVersion(ctx context.Context, docdag string) (string, error) {
	out, err := docdagOutput(ctx, docdag, "", "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// revision records the vault commit the documents were read at, with a
// `-dirty` suffix where the working tree had changes. A vault that is not a
// git repository at all renders fine and says `unversioned`: the harness a
// test builds in a temporary directory is exactly that.
func revision(ctx context.Context, vault string) (string, error) {
	head, err := gitOutput(ctx, vault, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "unversioned", nil //nolint:nilerr // a vault outside git is a legal vault
	}
	rev := strings.TrimSpace(head)
	status, err := gitOutput(ctx, vault, "status", "--porcelain")
	if err != nil {
		return rev, nil //nolint:nilerr // the revision is the useful half
	}
	if strings.TrimSpace(status) != "" {
		rev += "-dirty"
	}
	return rev, nil
}

// docdagOutput runs docdag in a directory and returns its standard output.
func docdagOutput(ctx context.Context, binary, dir string, args ...string) (string, error) {
	if binary == "" {
		binary = "docdag"
	}
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // the caller names the engine
	cmd.Dir = dir
	// LC_ALL is pinned on every child whose output is hashed or parsed: a
	// locale that changes a message changes bytes this reads.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s %s: %w: %s",
			ErrRender, binary, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// git runs a git command and discards its output.
func git(ctx context.Context, dir string, args ...string) error {
	out, err := gitOutput(ctx, dir, args...)
	if err != nil {
		return fmt.Errorf("%w: git %s: %s", ErrRender, strings.Join(args, " "), strings.TrimSpace(out))
	}
	return nil
}

// gitOutput runs a git command and returns standard output on success and
// standard error on failure, which is the half worth reading in each case.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// The environment is built rather than inherited, and that is not
	// tidiness: three channels change the bytes of the rendered tree, and so
	// tree_sha256, without touching the vault at all. GIT_CONFIG_COUNT is read
	// as command-line configuration and is not suppressed by nulling the
	// global file; $XDG_CONFIG_HOME/git/attributes is the default
	// core.attributesFile whatever the config says, so `* text=auto eol=crlf`
	// there rewrites every line ending; and a template directory can carry an
	// attributes file of its own. Worse, an inherited GIT_DIR — every git hook
	// exports one, and so does `git rebase --exec` — makes this stage the
	// harness's files into an unrelated repository.
	//
	// The scratch HOME is the directory above the tree, which this process
	// made and owns, so nothing a person has configured is reachable from it.
	scratch := filepath.Dir(dir)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"HOME=" + scratch,
		"XDG_CONFIG_HOME=" + filepath.Join(scratch, "xdg"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stderr.String(), err
	}
	return stdout.String(), nil
}
