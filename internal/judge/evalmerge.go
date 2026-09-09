package judge

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
)

// EvaluationLabelMerge keeps the merged labels and the items whose independent
// annotations still disagree. Unresolved items are deliberately retained in
// Labels so a human can add an adjudication without reconstructing provenance.
type EvaluationLabelMerge struct {
	Labels     []EvaluationLabel
	Unresolved []string
}

// LoadEvaluationLabelFile reads one completed label-import result. The caller
// supplies the file path; this function applies the same strict JSON reader as
// evaluation metadata so a second value or an unknown field cannot hide in an
// input claimed to be one annotator's work.
func LoadEvaluationLabelFile(path string) ([]EvaluationLabel, error) {
	file, err := os.Open(path) //nolint:gosec // caller names a completed input
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	defer func() { _ = file.Close() }()
	var labels []EvaluationLabel
	if err := decodeStrict(file, &labels); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrJudge, path, err)
	}
	return labels, nil
}

// MergeEvaluationLabels combines two or more complete one-annotator inputs.
// It never chooses between disagreeing annotations. Optional adjudications
// are checked as human decisions and attached only to the item they decide.
func MergeEvaluationLabels(items []EvaluationItem, inputs [][]EvaluationLabel, adjudications []EvaluationLabel) (EvaluationLabelMerge, error) {
	if len(inputs) < 2 {
		return EvaluationLabelMerge{}, fmt.Errorf("%w: label merge needs at least two annotator inputs", ErrJudge)
	}
	byID := make(map[string]EvaluationItem, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		if _, duplicate := byID[item.ID]; duplicate {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: evaluation items repeat %q", ErrJudge, item.ID)
		}
		byID[item.ID] = item
		order = append(order, item.ID)
	}
	if len(byID) == 0 {
		return EvaluationLabelMerge{}, fmt.Errorf("%w: label merge has no evaluation items", ErrJudge)
	}
	merged := make(map[string]EvaluationLabel, len(items))
	annotators := map[string]bool{}
	for source, labels := range inputs {
		if len(labels) != len(items) {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: annotator input %d has %d items, want %d", ErrJudge, source+1, len(labels), len(items))
		}
		seen := map[string]bool{}
		var annotator string
		for _, label := range labels {
			item, ok := byID[label.Item]
			if !ok || seen[label.Item] {
				return EvaluationLabelMerge{}, fmt.Errorf("%w: annotator input %d has invalid or repeated item %q", ErrJudge, source+1, label.Item)
			}
			seen[label.Item] = true
			if label.Adjudication != nil || len(label.Annotations) != 1 {
				return EvaluationLabelMerge{}, fmt.Errorf("%w: annotator input %d item %q is not one independent annotation", ErrJudge, source+1, label.Item)
			}
			if err := validateEvaluationLabelCandidates(label, item); err != nil {
				return EvaluationLabelMerge{}, err
			}
			annotation := label.Annotations[0]
			if err := validateEvaluationAnnotation(annotation, item); err != nil {
				return EvaluationLabelMerge{}, err
			}
			if annotator == "" {
				annotator = annotation.Annotator
			} else if annotator != annotation.Annotator {
				return EvaluationLabelMerge{}, fmt.Errorf("%w: annotator input %d mixes annotators %q and %q", ErrJudge, source+1, annotator, annotation.Annotator)
			}
			result := merged[label.Item]
			if result.Item != "" && (!sameCandidateInputs(result, label)) {
				return EvaluationLabelMerge{}, fmt.Errorf("%w: annotator inputs bind different task content for item %q", ErrJudge, label.Item)
			}
			result.Item = label.Item
			result.TaskSHA256 = label.TaskSHA256
			result.ConversationSHA256 = label.ConversationSHA256
			result.RubricSHA256 = label.RubricSHA256
			result.ReferenceSHA256 = label.ReferenceSHA256
			result.CandidateSHA256 = cloneCandidateSHA256(label.CandidateSHA256)
			result.Annotations = append(result.Annotations, normalizedAnnotation(annotation))
			merged[label.Item] = result
		}
		if annotators[annotator] {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: label merge repeats annotator %q", ErrJudge, annotator)
		}
		annotators[annotator] = true
	}
	adjudicated := map[string]EvaluationAnnotation{}
	for _, label := range adjudications {
		item, ok := byID[label.Item]
		if !ok || label.Adjudication == nil || len(label.Annotations) != 0 {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: invalid adjudication for item %q", ErrJudge, label.Item)
		}
		if _, duplicate := adjudicated[label.Item]; duplicate {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: adjudications repeat item %q", ErrJudge, label.Item)
		}
		if strings.TrimSpace(label.Adjudication.Rationale) == "" {
			return EvaluationLabelMerge{}, fmt.Errorf("%w: evaluation item %q adjudication has no rationale", ErrJudge, label.Item)
		}
		if err := validateEvaluationAnnotation(*label.Adjudication, item); err != nil {
			return EvaluationLabelMerge{}, err
		}
		adjudicated[label.Item] = normalizedAnnotation(*label.Adjudication)
	}
	result := EvaluationLabelMerge{Labels: make([]EvaluationLabel, 0, len(items))}
	for _, id := range order {
		label := merged[id]
		if adjudication, ok := adjudicated[id]; ok {
			label.Adjudication = &adjudication
		}
		if !annotationsAgree(label.Annotations) && label.Adjudication == nil {
			result.Unresolved = append(result.Unresolved, id)
		}
		result.Labels = append(result.Labels, label)
	}
	return result, nil
}

func sameCandidateInputs(left, right EvaluationLabel) bool {
	return left.TaskSHA256 == right.TaskSHA256 && left.ConversationSHA256 == right.ConversationSHA256 && left.RubricSHA256 == right.RubricSHA256 && left.ReferenceSHA256 == right.ReferenceSHA256 && maps.Equal(left.CandidateSHA256, right.CandidateSHA256)
}

func annotationsAgree(annotations []EvaluationAnnotation) bool {
	if len(annotations) < 2 {
		return false
	}
	first := annotations[0]
	for _, annotation := range annotations[1:] {
		if annotation.AllBad != first.AllBad || !slices.Equal(annotation.Acceptable, first.Acceptable) {
			return false
		}
	}
	return true
}

// ReadEvaluationAdjudications accepts an optional JSON array where each value
// has item and adjudication only. Keeping it in EvaluationLabel shape means a
// recorded decision can pass directly through the existing label validator.
func ReadEvaluationAdjudications(path string) ([]EvaluationLabel, error) {
	if path == "" {
		return nil, nil
	}
	return LoadEvaluationLabelFile(path)
}

// SortedUnresolved returns a stable copy for callers that do not retain item
// declaration order.
func (m EvaluationLabelMerge) SortedUnresolved() []string {
	out := slices.Clone(m.Unresolved)
	sort.Strings(out)
	return out
}
