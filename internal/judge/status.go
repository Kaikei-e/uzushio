package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Status is what the vault says about the judges it has measured.
type Status struct {
	// AsOf is the day the question was asked.
	AsOf string
	// Binding are the calibrations in force on that day, newest window first.
	Binding []doc.Calibration
	// LastHuman is the most recent calibration that measured validity at all,
	// binding or not — the one the staleness warning is about. Nil where the
	// vault holds none.
	LastHuman *doc.Calibration
	// DaysSinceHuman is how long ago that measurement's window closed.
	DaysSinceHuman int
	// Warnings are the sentences a person has to read.
	Warnings []string
}

// ReadStatus asks the graph which calibrations bind, and reads them.
//
// It asks DocDag rather than listing the directory, for the reason the whole
// vault exists: force is derived from the graph, and a second implementation
// of "binding" here would be a second answer. What the day does to a
// calibration — thirty days after the window closes it stops binding, with no
// document edited — is the entire mechanism behind the warning below.
func ReadStatus(ctx context.Context, docdag, vault, asOf string) (Status, error) {
	if asOf == "" {
		asOf = time.Now().UTC().Format(vocab.DayLayout)
	}
	binding, err := bindingCalibrations(ctx, docdag, vault, asOf)
	if err != nil {
		return Status{}, err
	}
	all, err := Calibrations(vault)
	if err != nil {
		return Status{}, err
	}
	return StatusOf(asOf, binding, all), nil
}

// StatusOf assembles the reading from an answer about what binds and the
// documents themselves. It is separate from ReadStatus so the warnings — which
// are the part worth getting right — can be argued about without an engine on
// the path.
func StatusOf(asOf string, binding []string, all []doc.Calibration) Status {
	status := Status{AsOf: asOf}
	for _, calibration := range all {
		if slices.Contains(binding, calibration.ID()) {
			status.Binding = append(status.Binding, calibration)
		}
	}
	sort.Slice(status.Binding, func(a, b int) bool {
		return status.Binding[a].WindowTo > status.Binding[b].WindowTo
	})

	for i, calibration := range all {
		if calibration.HumanKappa == doc.KappaUnmeasured {
			continue
		}
		if status.LastHuman == nil || calibration.WindowTo > status.LastHuman.WindowTo {
			status.LastHuman = &all[i]
		}
	}
	if status.LastHuman != nil {
		status.DaysSinceHuman = daysBetween(status.LastHuman.WindowTo, asOf)
	}

	switch {
	case status.LastHuman == nil:
		status.Warnings = append(status.Warnings,
			"validity has never been measured: no calibration in this vault compares a judge "+
				"with people, so nothing here says the judge is measuring the right thing")
	case status.DaysSinceHuman > doc.ValidityDays:
		// Strictly greater, and the boundary is the point: a calibration is in
		// force *through* window_to + ValidityDays, so on that day it is still
		// binding and there is nothing to warn about. The engine's
		// `period.until` is exclusive, which is why the frontmatter carries one
		// more day than this number; see doc.Calibration.InForceUntil.
		status.Warnings = append(status.Warnings, fmt.Sprintf(
			"validity not measured for %d days: the last comparison with people closed on %s "+
				"and a calibration is in force for %d days after that",
			status.DaysSinceHuman, status.LastHuman.WindowTo, doc.ValidityDays))
	}
	switch {
	case len(status.Binding) == 0:
		status.Warnings = append(status.Warnings,
			"no calibration is binding today: whatever the judge is deciding, it is deciding it "+
				"on a measurement that has expired or was never made")
	case !slices.ContainsFunc(status.Binding, func(c doc.Calibration) bool {
		return c.HumanKappa != doc.KappaUnmeasured
	}):
		status.Warnings = append(status.Warnings,
			"every binding calibration says human_kappa: unmeasured — the judge has been shown "+
				"consistent with itself and never compared with a person")
	}
	// A binding calibration that says the judge does not agree with people is
	// not a quiet state. It is the one the whole kind exists to make loud:
	// something is deciding with a judge the vault has measured and rejected.
	for _, calibration := range status.Binding {
		switch calibration.Verdict {
		case vocab.CalibratedYes:
			// The state the whole apparatus exists to reach. Nothing to say.
		case vocab.CalibratedNo:
			status.Warnings = append(status.Warnings, fmt.Sprintf(
				"%s is binding and says `%s`: agreement with people was measured at %s and did "+
					"not clear the threshold, so nothing should be deciding with this judge alone",
				calibration.ID(), calibration.Verdict, doc.Kappa(calibration.HumanKappa)))
		case vocab.CalibratedUnmeasured:
			status.Warnings = append(status.Warnings, fmt.Sprintf(
				"%s is binding and says `%s`: its consistency was measured and its agreement "+
					"with people was not", calibration.ID(), calibration.Verdict))
		}
	}
	return status
}

// Lines renders the status for a terminal.
func (s Status) Lines() []string {
	out := []string{fmt.Sprintf("binding calibrations as of %s: %d", s.AsOf, len(s.Binding))}
	for _, calibration := range s.Binding {
		// The last day it binds, not the exclusive day the frontmatter
		// carries: a line reading "in force until" a day the document is not
		// in force on is the off-by-one made visible.
		last, err := calibration.LastDay()
		if err != nil {
			last = "?"
		}
		out = append(out, fmt.Sprintf(
			"  %s  verdict=%s  human_kappa=%s (n=%d)  swap=%s  rerun=%s  in force through %s",
			calibration.ID(), calibration.Verdict, doc.Kappa(calibration.HumanKappa),
			calibration.NHuman, doc.Kappa(calibration.SwapKappa), doc.Kappa(calibration.RerunKappa),
			last))
		out = append(out, fmt.Sprintf("    tie handling: %s; report: %s",
			calibration.TieHandling, calibration.Report))
	}
	if s.LastHuman != nil {
		out = append(out, fmt.Sprintf("last measurement against people: %s (%d day(s) ago), kappa %s over %d item(s)",
			s.LastHuman.WindowTo, s.DaysSinceHuman, doc.Kappa(s.LastHuman.HumanKappa), s.LastHuman.NHuman))
	}
	for _, warning := range s.Warnings {
		out = append(out, "warning: "+warning)
	}
	return out
}

// bindingCalibrations asks DocDag which calibration documents bind on a day.
func bindingCalibrations(ctx context.Context, binary, vault, asOf string) ([]string, error) {
	type row struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if binary == "" {
		binary = "docdag"
	}
	cmd := exec.CommandContext(ctx, binary, //nolint:gosec // the caller names the engine
		"query", "--binding", "--fields", "id,path", "--format", "json", "--as-of", asOf)
	cmd.Dir = vault
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s query --binding: %w: %s", ErrJudge, binary, err, firstLine(errOut.String()))
	}
	var rows []row
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		return nil, fmt.Errorf("%w: %s query returned something that is not the JSON this reads: %w",
			ErrJudge, binary, err)
	}
	var ids []string
	for _, r := range rows {
		if path.Dir(r.Path) == vocab.DirCalibrations && vocab.ValidCalibrationID(r.ID) {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

// Calibrations reads every calibration document in the vault, in file order.
func Calibrations(vault string) ([]doc.Calibration, error) {
	dir := filepath.Join(vault, filepath.FromSlash(vocab.DirCalibrations))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var out []doc.Calibration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // a vault document
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrJudge, err)
		}
		calibration, err := doc.ParseCalibration(body)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrJudge, entry.Name(), err)
		}
		out = append(out, calibration)
	}
	return out, nil
}

// daysBetween is how many days later `to` is than `from`, zero where either is
// unreadable or the order is the other way round.
func daysBetween(from, to string) int {
	start, err := time.Parse(vocab.DayLayout, from)
	if err != nil {
		return 0
	}
	end, err := time.Parse(vocab.DayLayout, to)
	if err != nil {
		return 0
	}
	return max(int(end.Sub(start).Hours()/24), 0)
}
