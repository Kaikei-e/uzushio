package judge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// EvaluationDatasetSchemaVersion is the version of a P1 evaluation dataset.
const EvaluationDatasetSchemaVersion = 1

// EvaluationDataset identifies a fixed collection of labelled evaluation
// items. It deliberately holds metadata and candidate identifiers only; task
// text, traces, and answers remain in the evaluation harness.
type EvaluationDataset struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	Items         []EvaluationItem `json:"items"`
}

// EvaluationItem records an evaluation item's provenance and strata.
type EvaluationItem struct {
	ID                 string            `json:"id"`
	Set                string            `json:"set"`
	Source             string            `json:"source"`
	SourceID           string            `json:"source_id"`
	License            string            `json:"license"`
	Cluster            string            `json:"cluster"`
	ConversationSHA256 string            `json:"conversation_sha256"`
	Reason             string            `json:"reason"`
	Strata             map[string]string `json:"strata"`
	Candidates         []string          `json:"candidates"`
}

// EvaluationAnnotation is one person's assessment of an item's candidates.
type EvaluationAnnotation struct {
	Annotator  string   `json:"annotator"`
	Kind       string   `json:"kind"`
	Acceptable []string `json:"acceptable"`
	AllBad     bool     `json:"all_bad"`
	Rationale  string   `json:"rationale"`
}

// EvaluationLabel holds the independent annotations and any human
// adjudication for one item.
type EvaluationLabel struct {
	TaskSHA256         string                 `json:"task_sha256"`
	Item               string                 `json:"item"`
	ConversationSHA256 string                 `json:"conversation_sha256"`
	RubricSHA256       string                 `json:"rubric_sha256"`
	ReferenceSHA256    string                 `json:"reference_sha256,omitempty"`
	CandidateSHA256    map[string]string      `json:"candidate_sha256"`
	Annotations        []EvaluationAnnotation `json:"annotations"`
	Adjudication       *EvaluationAnnotation  `json:"adjudication"`
}

// ResolvedEvaluationLabel is the label an assessment may use after its
// independent annotations agree or a human adjudicates their disagreement.
type ResolvedEvaluationLabel struct {
	TaskSHA256         string            `json:"task_sha256"`
	Item               string            `json:"item"`
	ConversationSHA256 string            `json:"conversation_sha256"`
	RubricSHA256       string            `json:"rubric_sha256"`
	ReferenceSHA256    string            `json:"reference_sha256,omitempty"`
	CandidateSHA256    map[string]string `json:"candidate_sha256"`
	Acceptable         []string          `json:"acceptable"`
	AllBad             bool              `json:"all_bad"`
	Annotators         []string          `json:"annotators"`
	Adjudicator        string            `json:"adjudicator,omitempty"`
}

// LoadEvaluationDataset reads and validates a dataset manifest without opening
// any evaluation asset named by it.
func LoadEvaluationDataset(path string) (*EvaluationDataset, error) {
	file, err := os.Open(path) //nolint:gosec // the caller names the dataset
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	defer func() { _ = file.Close() }()

	var dataset EvaluationDataset
	if err := decodeStrict(file, &dataset); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrJudge, path, err)
	}
	if err := dataset.validate(); err != nil {
		return nil, err
	}
	return &dataset, nil
}

func decodeStrict(r io.Reader, into any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("more than one JSON value")
		}
		return err
	}
	return nil
}

func (d EvaluationDataset) validate() error {
	if d.SchemaVersion != EvaluationDatasetSchemaVersion {
		return fmt.Errorf("%w: evaluation dataset schema version %d, this build reads %d",
			ErrJudge, d.SchemaVersion, EvaluationDatasetSchemaVersion)
	}
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("%w: evaluation dataset has no id", ErrJudge)
	}
	ids := map[string]bool{}
	sources := map[string]bool{}
	conversations := map[string]bool{}
	clusters := map[string]string{}
	for _, item := range d.Items {
		if err := item.validate(); err != nil {
			return err
		}
		if ids[item.ID] {
			return fmt.Errorf("%w: evaluation dataset repeats item %q", ErrJudge, item.ID)
		}
		ids[item.ID] = true
		source := item.Source + "\x00" + item.SourceID
		if sources[source] {
			return fmt.Errorf("%w: evaluation dataset repeats source/source_id %q/%q", ErrJudge, item.Source, item.SourceID)
		}
		sources[source] = true
		if conversations[item.ConversationSHA256] {
			return fmt.Errorf("%w: evaluation dataset repeats conversation digest %q", ErrJudge, item.ConversationSHA256)
		}
		conversations[item.ConversationSHA256] = true
		if previous, ok := clusters[item.Cluster]; ok && previous != item.Set {
			return fmt.Errorf("%w: evaluation dataset cluster %q crosses %s and %s", ErrJudge, item.Cluster, previous, item.Set)
		}
		clusters[item.Cluster] = item.Set
	}
	return nil
}

func (i EvaluationItem) validate() error {
	for _, required := range []struct{ name, value string }{
		{"id", i.ID}, {"source", i.Source}, {"source_id", i.SourceID}, {"license", i.License},
		{"cluster", i.Cluster}, {"reason", i.Reason},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("%w: evaluation item has no %s", ErrJudge, required.name)
		}
	}
	if i.Set != "D" && i.Set != "R" && i.Set != "H" {
		return fmt.Errorf("%w: evaluation item %q has invalid set %q", ErrJudge, i.ID, i.Set)
	}
	if !validSHA256(i.ConversationSHA256) {
		return fmt.Errorf("%w: evaluation item %q has invalid conversation_sha256", ErrJudge, i.ID)
	}
	for _, key := range []string{"language", "category", "length_bin"} {
		if strings.TrimSpace(i.Strata[key]) == "" {
			return fmt.Errorf("%w: evaluation item %q has no strata.%s", ErrJudge, i.ID, key)
		}
	}
	if len(i.Candidates) < 2 {
		return fmt.Errorf("%w: evaluation item %q has fewer than two candidates", ErrJudge, i.ID)
	}
	seen := map[string]bool{}
	for _, candidate := range i.Candidates {
		if strings.TrimSpace(candidate) == "" || seen[candidate] {
			return fmt.Errorf("%w: evaluation item %q has invalid candidate id %q", ErrJudge, i.ID, candidate)
		}
		seen[candidate] = true
	}
	return nil
}

// ItemsFor returns the dataset's items in their declared order for one set.
func (d *EvaluationDataset) ItemsFor(set string) []EvaluationItem {
	if d == nil {
		return nil
	}
	out := make([]EvaluationItem, 0)
	for _, item := range d.Items {
		if item.Set == set {
			out = append(out, item)
		}
	}
	return out
}

// HoldoutDigest identifies the H membership independently of local item IDs.
// It hashes only provenance and conversation metadata, never the item assets
// or candidate text.
func (d *EvaluationDataset) HoldoutDigest() string {
	type holdout struct {
		Source             string `json:"source"`
		SourceID           string `json:"source_id"`
		ConversationSHA256 string `json:"conversation_sha256"`
		Cluster            string `json:"cluster"`
	}
	items := make([]holdout, 0)
	if d != nil {
		for _, item := range d.Items {
			if item.Set == "H" {
				items = append(items, holdout{item.Source, item.SourceID, item.ConversationSHA256, item.Cluster})
			}
		}
	}
	sort.Slice(items, func(a, b int) bool {
		left, _ := json.Marshal(items[a])
		right, _ := json.Marshal(items[b])
		return string(left) < string(right)
	})
	body, _ := json.Marshal(items)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// LoadEvaluationLabels reads and resolves the complete label set for items.
func LoadEvaluationLabels(path string, items []EvaluationItem, minAnnotators int) (map[string]ResolvedEvaluationLabel, error) {
	if minAnnotators < 2 {
		return nil, fmt.Errorf("%w: evaluation labels need at least two independent annotators", ErrJudge)
	}
	file, err := os.Open(path) //nolint:gosec // the caller names the labels
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	defer func() { _ = file.Close() }()
	var labels []EvaluationLabel
	if err := decodeStrict(file, &labels); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrJudge, path, err)
	}
	byItem := make(map[string]EvaluationItem, len(items))
	for _, item := range items {
		if _, duplicate := byItem[item.ID]; duplicate {
			return nil, fmt.Errorf("%w: evaluation items repeat %q", ErrJudge, item.ID)
		}
		byItem[item.ID] = item
	}
	resolved := make(map[string]ResolvedEvaluationLabel, len(items))
	for _, label := range labels {
		item, ok := byItem[label.Item]
		if !ok {
			return nil, fmt.Errorf("%w: evaluation labels name unknown item %q", ErrJudge, label.Item)
		}
		if _, duplicate := resolved[label.Item]; duplicate {
			return nil, fmt.Errorf("%w: evaluation labels repeat item %q", ErrJudge, label.Item)
		}
		value, err := resolveEvaluationLabel(label, item, minAnnotators)
		if err != nil {
			return nil, err
		}
		resolved[label.Item] = value
	}
	for item := range byItem {
		if _, ok := resolved[item]; !ok {
			return nil, fmt.Errorf("%w: evaluation labels omit item %q", ErrJudge, item)
		}
	}
	return resolved, nil
}

func resolveEvaluationLabel(label EvaluationLabel, item EvaluationItem, minAnnotators int) (ResolvedEvaluationLabel, error) {
	if strings.TrimSpace(label.Item) == "" {
		return ResolvedEvaluationLabel{}, fmt.Errorf("%w: evaluation label has no item", ErrJudge)
	}
	if err := validateEvaluationLabelCandidates(label, item); err != nil {
		return ResolvedEvaluationLabel{}, err
	}
	annotators := make([]string, 0, len(label.Annotations))
	values := make([]EvaluationAnnotation, 0, len(label.Annotations))
	seen := map[string]bool{}
	for _, annotation := range label.Annotations {
		if err := validateEvaluationAnnotation(annotation, item); err != nil {
			return ResolvedEvaluationLabel{}, err
		}
		if seen[annotation.Annotator] {
			return ResolvedEvaluationLabel{}, fmt.Errorf("%w: evaluation item %q repeats annotator %q", ErrJudge, item.ID, annotation.Annotator)
		}
		seen[annotation.Annotator] = true
		annotators = append(annotators, annotation.Annotator)
		values = append(values, normalizedAnnotation(annotation))
	}
	if len(annotators) < minAnnotators {
		return ResolvedEvaluationLabel{}, fmt.Errorf("%w: evaluation item %q has %d annotators, need %d", ErrJudge, item.ID, len(annotators), minAnnotators)
	}
	sort.Strings(annotators)
	answer := values[0]
	agreed := true
	for _, value := range values[1:] {
		if value.AllBad != answer.AllBad || !slices.Equal(value.Acceptable, answer.Acceptable) {
			agreed = false
			break
		}
	}
	if agreed {
		return resolvedEvaluationLabel(label, item.ID, answer, annotators, ""), nil
	}
	if label.Adjudication == nil {
		return ResolvedEvaluationLabel{}, fmt.Errorf("%w: evaluation item %q has disagreeing annotations without adjudication", ErrJudge, item.ID)
	}
	if strings.TrimSpace(label.Adjudication.Rationale) == "" {
		return ResolvedEvaluationLabel{}, fmt.Errorf("%w: evaluation item %q adjudication has no rationale", ErrJudge, item.ID)
	}
	if err := validateEvaluationAnnotation(*label.Adjudication, item); err != nil {
		return ResolvedEvaluationLabel{}, err
	}
	answer = normalizedAnnotation(*label.Adjudication)
	return resolvedEvaluationLabel(label, item.ID, answer, annotators, label.Adjudication.Annotator), nil
}

func resolvedEvaluationLabel(label EvaluationLabel, item string, answer EvaluationAnnotation, annotators []string, adjudicator string) ResolvedEvaluationLabel {
	return ResolvedEvaluationLabel{TaskSHA256: label.TaskSHA256, Item: item, ConversationSHA256: label.ConversationSHA256, RubricSHA256: label.RubricSHA256, ReferenceSHA256: label.ReferenceSHA256,
		CandidateSHA256: cloneCandidateSHA256(label.CandidateSHA256), Acceptable: answer.Acceptable, AllBad: answer.AllBad,
		Annotators: annotators, Adjudicator: adjudicator}
}

func validateEvaluationLabelCandidates(label EvaluationLabel, item EvaluationItem) error {
	if !validSHA256(label.TaskSHA256) || !validSHA256(label.ConversationSHA256) || !validSHA256(label.RubricSHA256) {
		return fmt.Errorf("%w: evaluation item %q label does not bind task, conversation, and rubric", ErrJudge, item.ID)
	}
	if label.ReferenceSHA256 != "" && !validSHA256(label.ReferenceSHA256) {
		return fmt.Errorf("%w: evaluation item %q label has invalid reference digest", ErrJudge, item.ID)
	}
	if len(label.CandidateSHA256) != len(item.Candidates) {
		return fmt.Errorf("%w: evaluation item %q label does not bind every candidate body", ErrJudge, item.ID)
	}
	for _, candidate := range item.Candidates {
		if !validSHA256(label.CandidateSHA256[candidate]) {
			return fmt.Errorf("%w: evaluation item %q label has invalid candidate digest for %q", ErrJudge, item.ID, candidate)
		}
	}
	for candidate := range label.CandidateSHA256 {
		if !slices.Contains(item.Candidates, candidate) {
			return fmt.Errorf("%w: evaluation item %q label binds unknown candidate %q", ErrJudge, item.ID, candidate)
		}
	}
	return nil
}

func cloneCandidateSHA256(values map[string]string) map[string]string {
	return maps.Clone(values)
}

// VerifyEvaluationLabelCandidates confirms that the candidate bytes presently
// named by a suite are exactly the bytes human annotators saw. Call it only
// after the holdout has been claimed, because it opens evaluation assets.
func VerifyEvaluationLabelCandidates(labels map[string]ResolvedEvaluationLabel, suite Suite, items []EvaluationItem) error {
	tasks := make(map[string]Task, len(suite.Tasks))
	for _, task := range suite.Tasks {
		tasks[task.ID] = task
	}
	for _, item := range items {
		label, ok := labels[item.ID]
		if !ok {
			return fmt.Errorf("%w: no resolved label for evaluation item %q", ErrJudge, item.ID)
		}
		if err := validateEvaluationLabelCandidates(EvaluationLabel{TaskSHA256: label.TaskSHA256, Item: item.ID, ConversationSHA256: label.ConversationSHA256, RubricSHA256: label.RubricSHA256, ReferenceSHA256: label.ReferenceSHA256, CandidateSHA256: label.CandidateSHA256}, item); err != nil {
			return err
		}
		task, ok := tasks[item.ID]
		if !ok {
			return fmt.Errorf("%w: suite %q has no evaluation item %q", ErrJudge, suite.ID, item.ID)
		}
		taskDir := suite.TaskDir(task)
		taskBody, err := os.ReadFile(filepath.Join(taskDir, "task.json")) //nolint:gosec // suite manifest names evaluation inputs
		if err != nil {
			return fmt.Errorf("%w: read task for %q: %w", ErrJudge, item.ID, err)
		}
		if digest(taskBody) != label.TaskSHA256 {
			return fmt.Errorf("%w: evaluation item %q task does not match its human label", ErrJudge, item.ID)
		}
		var spec taskFile
		if err := json.Unmarshal(taskBody, &spec); err != nil {
			return fmt.Errorf("%w: invalid task for %q: %w", ErrJudge, item.ID, err)
		}
		if spec.Conversation == "" || spec.Rubric == "" {
			return fmt.Errorf("%w: evaluation item %q task has no conversation or rubric", ErrJudge, item.ID)
		}
		conversation, err := os.ReadFile(filepath.Join(taskDir, filepath.FromSlash(spec.Conversation))) //nolint:gosec // suite task input
		if err != nil {
			return fmt.Errorf("%w: read conversation for %q: %w", ErrJudge, item.ID, err)
		}
		if digest(conversation) != label.ConversationSHA256 {
			return fmt.Errorf("%w: evaluation item %q conversation does not match its human label", ErrJudge, item.ID)
		}
		rubric, err := os.ReadFile(filepath.Join(taskDir, filepath.FromSlash(spec.Rubric))) //nolint:gosec // suite task input
		if err != nil {
			return fmt.Errorf("%w: read rubric for %q: %w", ErrJudge, item.ID, err)
		}
		if digest(rubric) != label.RubricSHA256 {
			return fmt.Errorf("%w: evaluation item %q rubric does not match its human label", ErrJudge, item.ID)
		}
		if spec.Reference == "" && label.ReferenceSHA256 != "" {
			return fmt.Errorf("%w: evaluation item %q label binds an absent reference", ErrJudge, item.ID)
		}
		if spec.Reference != "" {
			reference, err := os.ReadFile(filepath.Join(taskDir, filepath.FromSlash(spec.Reference))) //nolint:gosec // suite task input
			if err != nil {
				return fmt.Errorf("%w: read reference for %q: %w", ErrJudge, item.ID, err)
			}
			if label.ReferenceSHA256 == "" || digest(reference) != label.ReferenceSHA256 {
				return fmt.Errorf("%w: evaluation item %q reference does not match its human label", ErrJudge, item.ID)
			}
		}
		paths := suite.Candidates(task)
		if len(paths) != len(item.Candidates) {
			return fmt.Errorf("%w: suite %q has %d candidate bodies for %q, want %d", ErrJudge, suite.ID, len(paths), item.ID, len(item.Candidates))
		}
		for index, path := range paths {
			body, err := os.ReadFile(path) //nolint:gosec // suite manifest names evaluation inputs
			if err != nil {
				return fmt.Errorf("%w: read candidate %s: %w", ErrJudge, path, err)
			}
			candidate := item.Candidates[index]
			if digest(body) != label.CandidateSHA256[candidate] {
				return fmt.Errorf("%w: evaluation item %q candidate %q body does not match its human label", ErrJudge, item.ID, candidate)
			}
		}
	}
	return nil
}

func validateEvaluationAnnotation(annotation EvaluationAnnotation, item EvaluationItem) error {
	if strings.TrimSpace(annotation.Annotator) == "" || annotation.Kind != "human" {
		return fmt.Errorf("%w: evaluation item %q has non-human or unnamed annotation", ErrJudge, item.ID)
	}
	if annotation.AllBad && len(annotation.Acceptable) != 0 {
		return fmt.Errorf("%w: evaluation item %q marks all_bad with acceptable candidates", ErrJudge, item.ID)
	}
	if !annotation.AllBad && len(annotation.Acceptable) == 0 {
		return fmt.Errorf("%w: evaluation item %q has no acceptable candidates without all_bad", ErrJudge, item.ID)
	}
	allowed := map[string]bool{}
	for _, candidate := range item.Candidates {
		allowed[candidate] = true
	}
	seen := map[string]bool{}
	for _, candidate := range annotation.Acceptable {
		if !allowed[candidate] || seen[candidate] {
			return fmt.Errorf("%w: evaluation item %q has invalid acceptable candidate %q", ErrJudge, item.ID, candidate)
		}
		seen[candidate] = true
	}
	return nil
}

func normalizedAnnotation(annotation EvaluationAnnotation) EvaluationAnnotation {
	annotation.Acceptable = slices.Clone(annotation.Acceptable)
	sort.Strings(annotation.Acceptable)
	return annotation
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
