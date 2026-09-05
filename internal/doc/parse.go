package doc

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// The reader is the other half of the writers in this package, and it exists
// for one reason: the renderer has to know what an edit says about itself
// before it can put the edit's content anywhere. DocDag answers "which
// documents are binding" and nothing about their frontmatter — `query
// --fields` reads a key that writes as a list back as a placeholder — so the
// document is parsed here rather than queried.
//
// It is deliberately narrow. It reads an edit and nothing else, it reads the
// keys the edit kind declares and ignores the rest, and it hands back the same
// struct the writer takes, so the round trip is testable and a key that only
// one half knows about is a test failure rather than a silent drop.

// ParseEdit reads one edit document. The identifier comes from the file name's
// stem rather than from the bytes: an edit writes no `id:` key, because DocDag
// reads its identity off the file name, and a reader that invented one from
// the title would disagree with the graph.
func ParseEdit(id string, raw []byte) (Edit, error) {
	if !vocab.ValidEditID(id) {
		return Edit{}, fmt.Errorf("%w: %q is not an edit identifier (want %s)", ErrDocument, id, vocab.EditIDPattern)
	}
	front, title, body, err := split(raw)
	if err != nil {
		return Edit{}, fmt.Errorf("%w: edit %s: %w", ErrDocument, id, err)
	}
	var parsed EditFrontmatter
	if err := yaml.Unmarshal(front, &parsed); err != nil {
		return Edit{}, fmt.Errorf("%w: edit %s frontmatter: %w", ErrDocument, id, err)
	}
	if parsed.Kind != vocab.KindEdit.String() {
		return Edit{}, fmt.Errorf("%w: %s is kind %q, not an edit", ErrDocument, id, parsed.Kind)
	}
	edit := Edit{
		EditID:         id,
		Title:          parsed.Title,
		Date:           parsed.Date,
		Status:         vocab.Status(parsed.Status),
		Component:      parsed.Component,
		Touches:        parsed.Touches,
		Paths:          parsed.Paths,
		DiffSHA256:     parsed.DiffSHA256,
		RootCause:      parsed.RootCause,
		Approval:       vocab.Approval(parsed.Approval),
		ApprovedBy:     parsed.ApprovedBy,
		InForceFrom:    parsed.InForceFrom,
		InForceUntil:   parsed.InForceUntil,
		About:          parsed.About,
		Premise:        parsed.Premise,
		Counterexample: parsed.Counterexample,
		Body:           body,
	}
	// The title in the frontmatter and the title in the heading are the same
	// sentence written twice, and the writer writes them that way. A document
	// where they have drifted is one a reader would read differently from the
	// graph, so it is refused rather than silently preferred one way.
	if title != "" && title != parsed.Title {
		return Edit{}, fmt.Errorf("%w: edit %s heading %q does not match its title %q",
			ErrDocument, id, title, parsed.Title)
	}
	for _, entry := range parsed.Predicts {
		edit.Predicts = append(edit.Predicts, Prediction{
			Pattern: entry.Ref,
			Expect:  vocab.Expect(entry.Expect),
			Outcome: vocab.Outcome(entry.Outcome),
		})
	}
	for _, entry := range parsed.Supersedes {
		edit.Supersedes = append(edit.Supersedes, Supersession{Edit: entry.Ref, Reason: entry.Reason})
	}
	if err := edit.Validate(); err != nil {
		return Edit{}, err
	}
	return edit, nil
}

// split takes a document apart into its frontmatter, the text of its `#`
// heading, and the body under that heading — which for a memory or a skill
// edit is the content the renderer writes to a file, so where the heading
// stops matters and is not a matter of taste.
func split(raw []byte) (front []byte, title, body string, err error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	const fence = "---\n"
	if !strings.HasPrefix(text, fence) {
		return nil, "", "", fmt.Errorf("the document does not open with %q", "---")
	}
	rest := text[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return nil, "", "", fmt.Errorf("the frontmatter is not closed by %q on a line of its own", "---")
	}
	front = []byte(rest[:end+1])
	rest = rest[end+len("\n")+len(fence):]

	rest = strings.TrimLeft(rest, "\n")
	if strings.HasPrefix(rest, "# ") {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		rest = strings.TrimLeft(rest, "\n")
	}
	return front, title, strings.TrimRight(rest, "\n"), nil
}

// TitleOf returns the `#` heading of a document, for a caller that wants to
// name a document it is not otherwise reading.
func TitleOf(raw []byte) string {
	_, title, _, err := split(raw)
	if err != nil {
		return ""
	}
	return title
}

// bodyBytes is the content a memory or skill edit's document body becomes on
// disk: the body with exactly one trailing newline, which is what every other
// text file in these repositories ends with and what makes a later diff of the
// rendered tree read as a change to a line rather than to the file's end.
func bodyBytes(body string) []byte {
	trimmed := strings.TrimRight(body, "\n")
	if trimmed == "" {
		return nil
	}
	var out bytes.Buffer
	out.WriteString(trimmed)
	out.WriteByte('\n')
	return out.Bytes()
}

// Content returns the bytes a memory or skill edit puts at the single path it
// owns. A system-prompt edit has no content of its own — its content is the
// sidecar diff — and says so.
func (e Edit) Content() ([]byte, error) {
	if e.Component == componentSystemPrompt {
		return nil, fmt.Errorf("%w: edit %s is a system-prompt edit; its content is the sidecar diff, not the document body",
			ErrDocument, e.EditID)
	}
	return bodyBytes(e.Body), nil
}

// ParseCalibration reads one calibration document. Unlike an edit, a
// calibration carries its identifier in the frontmatter — the identifier holds
// a slash, which a file name's stem cannot — so the identifier is read from the
// bytes and taken apart into the parts the writer holds.
//
// The three coefficients come back as the numbers they are, with the word
// `unmeasured` reading back as KappaUnmeasured. A key that is neither a number
// nor that word is an error: a coefficient nobody can parse is worse than one
// nobody wrote.
func ParseCalibration(raw []byte) (Calibration, error) {
	front, title, body, err := split(raw)
	if err != nil {
		return Calibration{}, fmt.Errorf("%w: calibration: %w", ErrDocument, err)
	}
	var parsed CalibrationFrontmatter
	if err := yaml.Unmarshal(front, &parsed); err != nil {
		return Calibration{}, fmt.Errorf("%w: calibration frontmatter: %w", ErrDocument, err)
	}
	if parsed.Kind != vocab.KindCalibration.String() {
		return Calibration{}, fmt.Errorf("%w: %s is kind %q, not a calibration", ErrDocument, parsed.ID, parsed.Kind)
	}
	ref, err := vocab.ParseCalibrationID(parsed.ID)
	if err != nil {
		return Calibration{}, fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if title != "" && title != parsed.Title {
		return Calibration{}, fmt.Errorf("%w: calibration %s heading %q does not match its title %q",
			ErrDocument, parsed.ID, title, parsed.Title)
	}
	calibration := Calibration{
		Judge:       ref.Judge,
		Day:         ref.Day,
		Seq:         ref.Seq,
		Title:       parsed.Title,
		Date:        parsed.Date,
		Pool:        parsed.Pool,
		WindowFrom:  parsed.WindowFrom,
		WindowTo:    parsed.WindowTo,
		TieHandling: vocab.TieHandling(parsed.TieHandling),
		Verdict:     vocab.Calibrated(parsed.Verdict),
		Report:      parsed.Report,
		Body:        body,
	}
	for _, entry := range parsed.Supersedes {
		calibration.Supersedes = append(calibration.Supersedes,
			Supersession{Edit: entry.Ref, Reason: entry.Reason})
	}
	for _, count := range []struct {
		what string
		text string
		into *int
	}{
		{"n_items", parsed.NItems, &calibration.NItems},
		{"n_human", parsed.NHuman, &calibration.NHuman},
	} {
		value, err := strconv.Atoi(count.text)
		if err != nil {
			return Calibration{}, fmt.Errorf("%w: calibration %s %s %q is not a whole number",
				ErrDocument, parsed.ID, count.what, count.text)
		}
		*count.into = value
	}
	for _, kappa := range []struct {
		what string
		text string
		into *float64
	}{
		{"swap_kappa", parsed.SwapKappa, &calibration.SwapKappa},
		{"rerun_kappa", parsed.RerunKappa, &calibration.RerunKappa},
		{"human_kappa", parsed.HumanKappa, &calibration.HumanKappa},
	} {
		value, err := ParseKappa(kappa.text)
		if err != nil {
			return Calibration{}, fmt.Errorf("%w: calibration %s %s: %w", ErrDocument, parsed.ID, kappa.what, err)
		}
		*kappa.into = value
	}
	if err := calibration.Validate(); err != nil {
		return Calibration{}, err
	}
	return calibration, nil
}

// ParseKappa reads a coefficient back, with the word for the one that was
// never computed.
func ParseKappa(text string) (float64, error) {
	if text == vocab.KappaUnmeasured {
		return KappaUnmeasured, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is neither a number nor %q", ErrDocument, text, vocab.KappaUnmeasured)
	}
	return value, nil
}
