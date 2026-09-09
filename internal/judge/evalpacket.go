package judge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EvaluationPacketSchemaVersion is the version of the anonymous-label packet
// files. The mapping is deliberately separate from the material an annotator
// reads.
const EvaluationPacketSchemaVersion = 1

// EvaluationPacket is the material shown to one annotator. It contains no
// candidate identity, condition, selection result, or existing label.
type EvaluationPacket struct {
	SchemaVersion int                    `json:"schema_version"`
	DatasetID     string                 `json:"dataset_id"`
	Set           string                 `json:"set"`
	Annotator     string                 `json:"annotator"`
	Items         []EvaluationPacketItem `json:"items"`
}

// EvaluationPacketItem is one conversation, rubric, optional reference, and
// shuffled answer set exactly as its task.json names them.
type EvaluationPacketItem struct {
	Item         string                      `json:"item"`
	Conversation json.RawMessage             `json:"conversation"`
	Rubric       string                      `json:"rubric"`
	Reference    string                      `json:"reference,omitempty"`
	Candidates   []EvaluationPacketCandidate `json:"candidates"`
}

// EvaluationPacketCandidate has only an anonymous label and the unmodified
// candidate body.
type EvaluationPacketCandidate struct {
	Label string `json:"label"`
	Body  string `json:"body"`
}

// EvaluationPacketMapping is retained by the coordinator. It joins anonymous
// labels to candidate IDs and attests the exact source bytes used to make the
// packet.
type EvaluationPacketMapping struct {
	SchemaVersion int                           `json:"schema_version"`
	DatasetID     string                        `json:"dataset_id"`
	Set           string                        `json:"set"`
	Annotator     string                        `json:"annotator"`
	PacketSHA256  string                        `json:"packet_sha256"`
	Inputs        []EvaluationPacketInput       `json:"inputs"`
	Items         []EvaluationPacketMappingItem `json:"items"`
}

type EvaluationPacketInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type EvaluationPacketMappingItem struct {
	TaskSHA256         string                        `json:"task_sha256"`
	Item               string                        `json:"item"`
	ConversationSHA256 string                        `json:"conversation_sha256"`
	RubricSHA256       string                        `json:"rubric_sha256"`
	ReferenceSHA256    string                        `json:"reference_sha256,omitempty"`
	Candidates         []EvaluationPacketMappingPair `json:"candidates"`
}

type EvaluationPacketMappingPair struct {
	Label     string `json:"label"`
	Candidate string `json:"candidate"`
	SHA256    string `json:"sha256"`
}

// EvaluationPacketAnswers is the JSON file an annotator fills in. Its
// anonymous labels are translated only by ImportEvaluationPacket.
type EvaluationPacketAnswers struct {
	SchemaVersion int                          `json:"schema_version"`
	Annotator     string                       `json:"annotator"`
	MappingSHA256 string                       `json:"mapping_sha256"`
	PacketSHA256  string                       `json:"packet_sha256"`
	Items         []EvaluationPacketAnswerItem `json:"items"`
}

type EvaluationPacketAnswerItem struct {
	Item       string   `json:"item"`
	Acceptable []string `json:"acceptable"`
	AllBad     bool     `json:"all_bad"`
	Rationale  string   `json:"rationale"`
}

// BuildEvaluationPacket reads the dataset's named suite assets and returns a
// deterministic anonymous packet and coordinator mapping. It opens no gold or
// labels file.
func BuildEvaluationPacket(dataset *EvaluationDataset, suite Suite, set, annotator string, seed int64) (EvaluationPacket, EvaluationPacketMapping, error) {
	if dataset == nil {
		return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: nil evaluation dataset", ErrJudge)
	}
	if set != "D" && set != "R" && set != "H" {
		return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: invalid evaluation set %q", ErrJudge, set)
	}
	if strings.TrimSpace(annotator) == "" {
		return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: packet has no annotator", ErrJudge)
	}
	tasks := make(map[string]Task, len(suite.Tasks))
	for _, task := range suite.Tasks {
		tasks[task.ID] = task
	}
	packet := EvaluationPacket{SchemaVersion: EvaluationPacketSchemaVersion, DatasetID: dataset.ID, Set: set, Annotator: annotator}
	mapping := EvaluationPacketMapping{SchemaVersion: EvaluationPacketSchemaVersion, DatasetID: dataset.ID, Set: set, Annotator: annotator}
	rng := rand.New(rand.NewSource(seed))
	for _, item := range dataset.ItemsFor(set) {
		task, ok := tasks[item.ID]
		if !ok {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: suite %q has no evaluation item %q", ErrJudge, suite.ID, item.ID)
		}
		taskDir := suite.TaskDir(task)
		taskBody, err := readPacketInput(filepath.Join(taskDir, "task.json"), &mapping)
		if err != nil {
			return EvaluationPacket{}, EvaluationPacketMapping{}, err
		}
		var spec taskFile
		if err := json.Unmarshal(taskBody, &spec); err != nil {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation item %q task.json: %w", ErrJudge, item.ID, err)
		}
		if spec.Conversation == "" || spec.Rubric == "" {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation item %q task.json has no conversation or rubric", ErrJudge, item.ID)
		}
		conversationPath := filepath.Join(taskDir, filepath.FromSlash(spec.Conversation))
		conversation, err := readPacketInput(conversationPath, &mapping)
		if err != nil {
			return EvaluationPacket{}, EvaluationPacketMapping{}, err
		}
		if digest(conversation) != item.ConversationSHA256 {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation item %q conversation digest does not match dataset", ErrJudge, item.ID)
		}
		if !json.Valid(conversation) {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation item %q conversation is not JSON", ErrJudge, item.ID)
		}
		rubric, err := readPacketInput(filepath.Join(taskDir, filepath.FromSlash(spec.Rubric)), &mapping)
		if err != nil {
			return EvaluationPacket{}, EvaluationPacketMapping{}, err
		}
		var reference []byte
		if spec.Reference != "" {
			reference, err = readPacketInput(filepath.Join(taskDir, filepath.FromSlash(spec.Reference)), &mapping)
			if err != nil {
				return EvaluationPacket{}, EvaluationPacketMapping{}, err
			}
		}
		paths := suite.Candidates(task)
		if len(paths) != len(item.Candidates) {
			return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation item %q has %d dataset candidates, suite supplies %d", ErrJudge, item.ID, len(item.Candidates), len(paths))
		}
		order := rng.Perm(len(paths))
		packetItem := EvaluationPacketItem{Item: item.ID, Conversation: conversation, Rubric: string(rubric), Reference: string(reference)}
		mappingItem := EvaluationPacketMappingItem{TaskSHA256: digest(taskBody), Item: item.ID, ConversationSHA256: digest(conversation), RubricSHA256: digest(rubric), ReferenceSHA256: digestOptional(reference)}
		for anonymous, index := range order {
			body, err := readPacketInput(paths[index], &mapping)
			if err != nil {
				return EvaluationPacket{}, EvaluationPacketMapping{}, err
			}
			label := anonymousLabel(anonymous)
			packetItem.Candidates = append(packetItem.Candidates, EvaluationPacketCandidate{Label: label, Body: string(body)})
			mappingItem.Candidates = append(mappingItem.Candidates, EvaluationPacketMappingPair{Label: label, Candidate: item.Candidates[index], SHA256: digest(body)})
		}
		packet.Items = append(packet.Items, packetItem)
		mapping.Items = append(mapping.Items, mappingItem)
	}
	if len(packet.Items) == 0 {
		return EvaluationPacket{}, EvaluationPacketMapping{}, fmt.Errorf("%w: evaluation dataset has no %s items", ErrJudge, set)
	}
	return packet, mapping, nil
}

func readPacketInput(path string, mapping *EvaluationPacketMapping) ([]byte, error) {
	body, err := os.ReadFile(path) //nolint:gosec // suite manifest names its inputs
	if err != nil {
		return nil, fmt.Errorf("%w: read packet input %s: %w", ErrJudge, path, err)
	}
	mapping.Inputs = append(mapping.Inputs, EvaluationPacketInput{Path: path, SHA256: digest(body)})
	return body, nil
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func digestOptional(body []byte) string {
	if body == nil {
		return ""
	}
	return digest(body)
}

func anonymousLabel(index int) string {
	return string(rune('A' + index))
}

// WriteEvaluationPacket creates a new directory holding the annotator-facing
// Markdown and answer template, plus the coordinator-only mapping.
func WriteEvaluationPacket(out string, packet EvaluationPacket, mapping EvaluationPacketMapping) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%w: packet output %s already exists", ErrJudge, out)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%w: inspect packet output %s: %w", ErrJudge, out, err)
	}
	markdown := renderEvaluationPacket(packet)
	mapping.PacketSHA256 = digest([]byte(markdown))
	mappingBody, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode packet mapping: %w", ErrJudge, err)
	}
	answers := EvaluationPacketAnswers{SchemaVersion: EvaluationPacketSchemaVersion, Annotator: packet.Annotator,
		MappingSHA256: digest(mappingBody), PacketSHA256: mapping.PacketSHA256}
	for _, item := range packet.Items {
		answers.Items = append(answers.Items, EvaluationPacketAnswerItem{Item: item.Item})
	}
	answersBody, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode packet answers: %w", ErrJudge, err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("%w: create packet parent: %w", ErrJudge, err)
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		return fmt.Errorf("%w: create packet output %s: %w", ErrJudge, out, err)
	}
	for _, file := range []struct {
		name string
		body []byte
	}{
		{"packet.md", []byte(markdown)}, {"answers.json", append(answersBody, '\n')}, {"mapping.json", append(mappingBody, '\n')},
	} {
		if err := os.WriteFile(filepath.Join(out, file.name), file.body, 0o644); err != nil {
			return fmt.Errorf("%w: write packet %s: %w", ErrJudge, file.name, err)
		}
	}
	return nil
}

func renderEvaluationPacket(packet EvaluationPacket) string {
	var b strings.Builder
	b.WriteString("# Anonymous evaluation packet\n\n")
	b.WriteString("Choose every acceptable answer, or mark all_bad if none is acceptable. Give a rationale.\n")
	for _, item := range packet.Items {
		fmt.Fprintf(&b, "\n## Item %s\n\n### Conversation\n```json\n%s\n```\n\n### Rubric\n%s\n", item.Item, item.Conversation, item.Rubric)
		if item.Reference != "" {
			fmt.Fprintf(&b, "\n### Reference\n\n%s\n", item.Reference)
		}
		for _, candidate := range item.Candidates {
			fmt.Fprintf(&b, "\n### Answer %s\n\n%s\n", candidate.Label, candidate.Body)
		}
	}
	return b.String()
}

// ImportEvaluationPacket verifies a filled answer file against the packet's
// mapping and source digests, then translates its anonymous labels into one
// human annotation per item. It never manufactures a second annotation or an
// adjudication.
func ImportEvaluationPacket(packetDir, answersPath string) ([]EvaluationLabel, error) {
	mappingBody, err := os.ReadFile(filepath.Join(packetDir, "mapping.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: read packet mapping: %w", ErrJudge, err)
	}
	var mapping EvaluationPacketMapping
	if err := decodeStrict(strings.NewReader(string(mappingBody)), &mapping); err != nil {
		return nil, fmt.Errorf("%w: invalid packet mapping: %w", ErrJudge, err)
	}
	if mapping.SchemaVersion != EvaluationPacketSchemaVersion || strings.TrimSpace(mapping.Annotator) == "" {
		return nil, fmt.Errorf("%w: invalid packet mapping metadata", ErrJudge)
	}
	packet, err := os.ReadFile(filepath.Join(packetDir, "packet.md"))
	if err != nil || digest(packet) != mapping.PacketSHA256 {
		return nil, fmt.Errorf("%w: packet markdown does not match mapping", ErrJudge)
	}
	for _, input := range mapping.Inputs {
		body, err := os.ReadFile(input.Path) //nolint:gosec // mapping records packet inputs
		if err != nil || digest(body) != input.SHA256 {
			return nil, fmt.Errorf("%w: packet input %s does not match mapping", ErrJudge, input.Path)
		}
	}
	body, err := os.ReadFile(answersPath) //nolint:gosec // caller names answer file
	if err != nil {
		return nil, fmt.Errorf("%w: read packet answers: %w", ErrJudge, err)
	}
	var answers EvaluationPacketAnswers
	if err := decodeStrict(strings.NewReader(string(body)), &answers); err != nil {
		return nil, fmt.Errorf("%w: invalid packet answers: %w", ErrJudge, err)
	}
	if answers.SchemaVersion != EvaluationPacketSchemaVersion || answers.Annotator != mapping.Annotator ||
		answers.MappingSHA256 != digest(trimTrailingNewline(mappingBody)) || answers.PacketSHA256 != mapping.PacketSHA256 {
		return nil, fmt.Errorf("%w: packet answers do not match mapping", ErrJudge)
	}
	if len(answers.Items) != len(mapping.Items) {
		return nil, fmt.Errorf("%w: packet answers omit or add items", ErrJudge)
	}
	byItem := make(map[string]EvaluationPacketMappingItem, len(mapping.Items))
	for _, item := range mapping.Items {
		if _, duplicate := byItem[item.Item]; duplicate {
			return nil, fmt.Errorf("%w: packet mapping repeats item %q", ErrJudge, item.Item)
		}
		byItem[item.Item] = item
	}
	labels := make([]EvaluationLabel, 0, len(answers.Items))
	seen := map[string]bool{}
	for _, answer := range answers.Items {
		mappingItem, ok := byItem[answer.Item]
		if !ok || seen[answer.Item] {
			return nil, fmt.Errorf("%w: packet answers name invalid item %q", ErrJudge, answer.Item)
		}
		seen[answer.Item] = true
		if strings.TrimSpace(answer.Rationale) == "" {
			return nil, fmt.Errorf("%w: packet answer %q has no rationale", ErrJudge, answer.Item)
		}
		if !validSHA256(mappingItem.TaskSHA256) || !validSHA256(mappingItem.ConversationSHA256) || !validSHA256(mappingItem.RubricSHA256) || mappingItem.ReferenceSHA256 != "" && !validSHA256(mappingItem.ReferenceSHA256) {
			return nil, fmt.Errorf("%w: invalid packet mapping inputs for item %q", ErrJudge, answer.Item)
		}
		candidateByLabel := map[string]string{}
		candidateSHA256 := map[string]string{}
		for _, pair := range mappingItem.Candidates {
			if pair.Label == "" || pair.Candidate == "" || !validSHA256(pair.SHA256) || candidateByLabel[pair.Label] != "" || candidateSHA256[pair.Candidate] != "" {
				return nil, fmt.Errorf("%w: invalid packet mapping for item %q", ErrJudge, answer.Item)
			}
			candidateByLabel[pair.Label] = pair.Candidate
			candidateSHA256[pair.Candidate] = pair.SHA256
		}
		annotation := EvaluationAnnotation{Annotator: mapping.Annotator, Kind: "human", AllBad: answer.AllBad, Rationale: answer.Rationale}
		for _, label := range answer.Acceptable {
			candidate, ok := candidateByLabel[label]
			if !ok {
				return nil, fmt.Errorf("%w: packet answer %q has invalid anonymous label %q", ErrJudge, answer.Item, label)
			}
			annotation.Acceptable = append(annotation.Acceptable, candidate)
		}
		if annotation.AllBad && len(annotation.Acceptable) != 0 || !annotation.AllBad && len(annotation.Acceptable) == 0 {
			return nil, fmt.Errorf("%w: packet answer %q has invalid acceptable/all_bad choice", ErrJudge, answer.Item)
		}
		if hasDuplicate(annotation.Acceptable) {
			return nil, fmt.Errorf("%w: packet answer %q repeats an acceptable label", ErrJudge, answer.Item)
		}
		sort.Strings(annotation.Acceptable)
		labels = append(labels, EvaluationLabel{TaskSHA256: mappingItem.TaskSHA256, Item: answer.Item, ConversationSHA256: mappingItem.ConversationSHA256, RubricSHA256: mappingItem.RubricSHA256, ReferenceSHA256: mappingItem.ReferenceSHA256, CandidateSHA256: candidateSHA256, Annotations: []EvaluationAnnotation{annotation}})
	}
	return labels, nil
}

func trimTrailingNewline(body []byte) []byte { return []byte(strings.TrimSuffix(string(body), "\n")) }

func hasDuplicate(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}
