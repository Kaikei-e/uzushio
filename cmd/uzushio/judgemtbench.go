package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/pairwise"
)

// This file is the one place in uzushio that names a dataset. Everything it
// leans on — reading a paged rows endpoint, deriving three-way items from
// pairwise comparisons, writing a chat suite — is in internal/pairwise and
// speaks of "pairwise human judgments" and "a rows endpoint". What is here is
// the adapter: the field names of one corpus, its licence, and the wording of
// its attribution.

// The source, verbatim as the dataset card spells it.
const (
	mtbDataset = "lmsys/mt_bench_human_judgments"
	mtbConfig  = "default"
	mtbSplit   = "human"
	mtbLicense = "CC-BY-4.0"
	mtbPaper   = "arXiv:2306.05685"
	// mtbEndpoint is the datasets-server rows API. It is a flag's default
	// rather than a constant used directly, so a test can point the import at
	// a local server.
	mtbEndpoint = "https://datasets-server.huggingface.co/rows"
)

// mtbRow is one row of the split. The three keys a derivation needs are the
// prompt's identity, the two systems, and the verdict; the conversations carry
// the answers.
type mtbRow struct {
	QuestionID    int       `json:"question_id"`
	ModelA        string    `json:"model_a"`
	ModelB        string    `json:"model_b"`
	Winner        string    `json:"winner"`
	Judge         string    `json:"judge"`
	Turn          int       `json:"turn"`
	ConversationA []mtbTurn `json:"conversation_a"`
	ConversationB []mtbTurn `json:"conversation_b"`
}

// mtbTurn is one turn of one system's conversation.
type mtbTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// The verdict words this corpus uses.
const (
	mtbWinnerA = "model_a"
	mtbWinnerB = "model_b"
)

// newJudgeImportMTBenchCmd builds the import command.
func newJudgeImportMTBenchCmd() *cobra.Command {
	var (
		out            string
		endpoint       string
		minJudgments   int
		target         int
		seed           uint64
		limit          int
		suiteID        string
		candidatesOnly bool
	)
	cmd := &cobra.Command{
		Use:   "import-mtbench",
		Short: "Derive a chat calibration suite from the MT-Bench human judgments",
		Long: "import-mtbench reads " + mtbDataset + " (split `" + mtbSplit + "`) through the\n" +
			"Hugging Face datasets-server rows API and writes a calibration suite.\n\n" +
			"The source is pairwise: two answers to one prompt and a person's verdict on\n" +
			"which is better. A judge that picks one of three answers cannot be measured on\n" +
			"those directly, so each item here is a triple of systems whose three pairwise\n" +
			"majorities are all present and do not contradict each other, labelled with the\n" +
			"system that beat both others. Triples whose majorities cycle are discarded\n" +
			"rather than resolved — they have no true answer — and the discard rate is\n" +
			"written into DERIVATION.md, because it bounds the agreement any judge could\n" +
			"reach on this corpus.\n\n" +
			"The answers are reproduced verbatim. They are model outputs from 2023 and are\n" +
			"neither this repository's to edit nor this repository's to redistribute, so\n" +
			"they are written to disk and never committed. A fresh clone has the suite and\n" +
			"no answers in it; this command is how the answers arrive.\n\n" +
			"With --candidates-only it fills in the answers of a suite that is already on\n" +
			"disk and touches nothing else. The same --seed over the same source yields the\n" +
			"same items, and it refuses to write unless the items it derived are exactly the\n" +
			"ones the manifest lists — answers written into a differently sampled suite\n" +
			"would sit beside gold labels that belong to other answers.\n\n" +
			"The suite is licensed " + mtbLicense + " by way of its source; see ATTRIBUTION.md.\n" +
			"The import is deterministic given --seed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			errOut := cmd.ErrOrStderr()
			rows, err := pairwise.Rows{
				Endpoint: endpoint,
				Dataset:  mtbDataset,
				Config:   mtbConfig,
				Split:    mtbSplit,
				Limit:    limit,
				Log:      func(line string) { fmt.Fprintln(errOut, line) },
			}.All(cmd.Context())
			if err != nil {
				return err
			}
			corpus, err := mtbCorpus(rows)
			if err != nil {
				return err
			}
			items, stats, err := pairwise.Derive(corpus, pairwise.Options{
				MinJudgments: minJudgments,
				Target:       target,
				Seed:         seed,
			})
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return fmt.Errorf("the derivation kept nothing: %d triple(s) considered, "+
					"%d missing a pair, %d cyclic", stats.Triples, stats.Incomplete, stats.Cyclic)
			}
			command := fmt.Sprintf(
				"uzushio judge import-mtbench --out %s --min-judgments %d --target %d --seed %d",
				out, minJudgments, target, seed)
			provenance := pairwise.Provenance{
				SuiteID:     suiteID,
				Source:      mtbDataset,
				License:     mtbLicense,
				Attribution: mtbAttribution(),
				Command:     command,
				Notes:       mtbNotes(),
				ID:          mtbID,
				GoldExtra:   mtbGoldExtra,
				Rubric:      mtbRubric,
			}
			if candidatesOnly {
				if err := pairwise.WriteCandidates(out, items, provenance); err != nil {
					return err
				}
				fmt.Fprintf(errOut, "filled in the answers of %d item(s) in %s\n", len(items), out)
				fmt.Fprintln(cmd.OutOrStdout(), out)
				return nil
			}
			if err := pairwise.Write(out, items, stats, provenance); err != nil {
				return err
			}
			fmt.Fprintf(errOut,
				"%d prompt(s), %d comparison(s); %d triple(s) considered, %d incomplete, %d cyclic, "+
					"%d eligible, %d written to %s\n",
				stats.Groups, stats.Votes, stats.Triples, stats.Incomplete, stats.Cyclic,
				stats.Eligible, stats.Sampled, out)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "examples/suite-chat", "where the suite is written")
	cmd.Flags().StringVar(&endpoint, "endpoint", mtbEndpoint, "the rows API to read the split from")
	cmd.Flags().IntVar(&minJudgments, "min-judgments", 3,
		"the fewest human comparisons a triple may rest on, over its three pairs")
	cmd.Flags().IntVar(&target, "target", 200, "how many items to sample")
	cmd.Flags().Uint64Var(&seed, "seed", 1, "fixes the sampling and the candidate order")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many rows; 0 reads the split")
	cmd.Flags().StringVar(&suiteID, "suite-id", "suite-chat", "the identifier the suite carries")
	cmd.Flags().BoolVar(&candidatesOnly, "candidates-only", false,
		"fill in the answers of a suite already on disk and change nothing else")
	return cmd
}

// mtbCorpus turns rows into the corpus the derivation reads.
//
// A group is one (question, turn) pair. The conversation is the first user
// turn and nothing else; everything a system did in reply is inside its own
// candidate file.
//
// That shape is forced by what the human labels are labels of. On the second
// turn the annotator read one model's whole two-turn conversation against
// another's, and the second turns are usually critiques of the first ("Take a
// moment to evaluate and critique your own response") — so a judge shown only
// the two user turns is ranking three critiques of three answers it has not
// seen, and each of the three is critiquing something different. It would be
// estimating a different quantity from the one the gold label measures, on the
// half of the corpus that is turn two. So the candidate for a turn-two item is
// the model's own transcript: its first answer, the shared second question,
// and its second answer. The task's rubric says so, because a judge told
// nothing would read the transcript as one answer that repeats itself.
func mtbCorpus(rows []json.RawMessage) (*pairwise.Corpus, error) {
	corpus := pairwise.NewCorpus()
	for i, raw := range rows {
		var row mtbRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if row.Turn < 1 {
			return nil, fmt.Errorf("row %d: turn %d", i, row.Turn)
		}
		answer := 2*row.Turn - 1
		if len(row.ConversationA) <= answer || len(row.ConversationB) <= answer {
			return nil, fmt.Errorf("row %d: a conversation is too short for turn %d", i, row.Turn)
		}
		shared := []pairwise.Message{{
			Role:    row.ConversationA[0].Role,
			Content: row.ConversationA[0].Content,
		}}
		winner := pairwise.WinnerTie
		switch row.Winner {
		case mtbWinnerA:
			winner = pairwise.WinnerFirst
		case mtbWinnerB:
			winner = pairwise.WinnerSecond
		}
		vote := pairwise.Vote{
			First: row.ModelA, Second: row.ModelB, Winner: winner, Labeler: row.Judge,
		}
		group := fmt.Sprintf("%03d-t%d", row.QuestionID, row.Turn)
		if err := corpus.Add(group, shared, vote,
			mtbCandidate(row.ConversationA, row.Turn),
			mtbCandidate(row.ConversationB, row.Turn)); err != nil {
			return nil, err
		}
	}
	return corpus, nil
}

// mtbCandidate renders one system's side of an item.
//
// On the first turn that is its answer, verbatim. On the second it is the
// transcript the annotator read: the first answer, the shared second question,
// and the second answer, joined by the two markers the rubric names. Nothing
// inside an answer is touched — the joining happens between them.
func mtbCandidate(conversation []mtbTurn, turn int) string {
	if turn < 2 {
		return conversation[1].Content
	}
	var b strings.Builder
	for at := 1; at < 2*turn; at++ {
		if at > 1 {
			b.WriteString("\n\n")
			if conversation[at].Role == "user" {
				b.WriteString(mtbUserMarker + "\n")
			} else {
				b.WriteString(mtbAssistantMarker + "\n")
			}
		}
		b.WriteString(conversation[at].Content)
	}
	return b.String()
}

// The markers a multi-turn candidate is joined with. They are plain words in
// square brackets rather than anything resembling the judge's own framing:
// a candidate block is data, and a marker that looked like a role header in
// the judge's prompt would be an instruction the corpus smuggled in.
const (
	mtbUserMarker      = "[user]"
	mtbAssistantMarker = "[assistant]"
)

// mtbRubric is the judge-only note a multi-turn item carries.
func mtbRubric(item pairwise.Item) string {
	if _, turn := mtbCoordinates(item); turn > 1 {
		return "Each candidate is a **two-turn transcript**, not a single answer.\n\n" +
			"It holds the assistant's answer to the question above, then the marker `" +
			mtbUserMarker + "` and the follow-up question every candidate was asked, then\n" +
			"the marker `" + mtbAssistantMarker + "` and the assistant's answer to it.\n\n" +
			"Judge the transcript as a whole: the follow-up answer usually depends on the\n" +
			"first one, and the candidates' first answers differ. The markers are\n" +
			"structure, not content — do not reward or penalise a candidate for them.\n"
	}
	return ""
}

// mtbID names an item's task directory: the corpus, the prompt, and a letter
// for the triple, since one prompt yields several.
func mtbID(item pairwise.Item) (string, error) {
	return "mtb-" + item.Group + "-" + letter(item.Ordinal), nil
}

// letter turns an index into a lowercase suffix, rolling over into two letters
// past the twenty-sixth so an identifier is never ambiguous.
func letter(n int) string {
	if n < 26 {
		return string(rune('a' + n))
	}
	return string(rune('a'+n/26-1)) + string(rune('a'+n%26))
}

// mtbGoldExtra writes the source's own coordinates into a gold file, so an
// item can be traced back to the rows it came from.
func mtbGoldExtra(item pairwise.Item) []pairwise.Field {
	question, turn := mtbCoordinates(item)
	if turn == 0 {
		return []pairwise.Field{{Key: "group", Value: item.Group}}
	}
	return []pairwise.Field{
		{Key: "question_id", Value: question},
		{Key: "turn", Value: turn},
	}
}

// mtbCoordinates reads the prompt and the turn back out of a group name. It
// answers with a zero turn where the name is not one this adapter wrote.
func mtbCoordinates(item pairwise.Item) (question, turn int) {
	if _, err := fmt.Sscanf(item.Group, "%d-t%d", &question, &turn); err != nil {
		return 0, 0
	}
	return question, turn
}

// mtbAttribution is the whole of ATTRIBUTION.md. It is written out rather than
// templated: an attribution is a statement about somebody else's licence, and
// the wording is the substance.
func mtbAttribution() string {
	var b strings.Builder
	b.WriteString("# Attribution\n\n")
	b.WriteString("The items in this directory are derived from **`" + mtbDataset + "`**,\n")
	b.WriteString("published on the Hugging Face Hub at\n")
	b.WriteString("<https://huggingface.co/datasets/" + mtbDataset + ">.\n\n")
	b.WriteString("The dataset card declares `license: cc-by-4.0`, so the source and this\n")
	b.WriteString("derivation are both under the **Creative Commons Attribution 4.0\n")
	b.WriteString("International** licence: <https://creativecommons.org/licenses/by/4.0/>.\n\n")
	b.WriteString("The dataset accompanies:\n\n")
	b.WriteString("> Lianmin Zheng, Wei-Lin Chiang, Ying Sheng, Siyuan Zhuang, Zhanghao Wu,\n")
	b.WriteString("> Yonghao Zhuang, Zi Lin, Zhuohan Li, Dacheng Li, Eric P. Xing, Hao Zhang,\n")
	b.WriteString("> Joseph E. Gonzalez and Ion Stoica.\n")
	b.WriteString("> *Judging LLM-as-a-Judge with MT-Bench and Chatbot Arena.*\n")
	b.WriteString("> " + mtbPaper + ".\n\n")
	b.WriteString("## What was taken, and what was changed\n\n")
	b.WriteString("Taken: the human `winner` verdicts of the `" + mtbSplit + "` split, the answers the\n")
	b.WriteString("compared models gave, and the questions they answered.\n\n")
	b.WriteString("Changed: the pairwise verdicts were aggregated into three-way labels, and\n")
	b.WriteString("the items were sampled. `DERIVATION.md` states exactly how, with the counts.\n")
	b.WriteString("CC BY 4.0 asks that changes be indicated; that file is the indication.\n\n")
	b.WriteString("Not changed: every `candidates/*.txt` file holds one model's answer as the\n")
	b.WriteString("dataset carries it, byte for byte apart from a trailing newline and, on a\n")
	b.WriteString("second-turn item, the two markers that join a model's two answers into the\n")
	b.WriteString("transcript the annotator read. They are outputs of the models named in each\n")
	b.WriteString("item's `gold.json`, produced in 2023, and this repository does not edit them —\n")
	b.WriteString("a corpus whose answers have been tidied is not the corpus the human labels\n")
	b.WriteString("were collected on.\n\n")
	b.WriteString("## What is committed here, and what is not\n\n")
	b.WriteString("Committed: `suite.json`, and per item `task.json`, `conversation.json`,\n")
	b.WriteString("`gold.json` and `rubric.md`. Those are the prompts people wrote and the\n")
	b.WriteString("judgements people made — the CC BY 4.0 part, redistributed under that licence\n")
	b.WriteString("with this attribution.\n\n")
	b.WriteString("**Not committed: `candidates/*.txt`.** Those are responses generated by the\n")
	b.WriteString("models named in each `gold.json`, and a model's output is subject to its\n")
	b.WriteString("provider's terms of use whatever licence the surrounding dataset carries.\n")
	b.WriteString("This repository does not redistribute them. They are fetched from the source\n")
	b.WriteString("onto the machine that needs them by\n\n")
	b.WriteString("```sh\nuzushio judge import-mtbench --candidates-only --out <this directory>\n```\n\n")
	b.WriteString("with the same `--seed` and `--target` the suite was derived at; `DERIVATION.md`\n")
	b.WriteString("records the command. The fetch refuses to write unless the items it derives\n")
	b.WriteString("are exactly the ones the manifest lists.\n")
	return b.String()
}

// mtbNotes are the paragraphs DERIVATION.md carries about this source in
// particular.
func mtbNotes() []string {
	return []string{
		"A group is one (`question_id`, `turn`) pair. Every model in the source answered\n" +
			"every question, which is why three-way items can be derived at all: a corpus\n" +
			"where each prompt was shown to one pair of models has no triples in it.",
		"The conversation a task carries is the **first user turn** and nothing else.\n" +
			"On a second-turn item each candidate file holds that model's whole side of the\n" +
			"exchange: its first answer, the marker `" + mtbUserMarker + "`, the shared follow-up\n" +
			"question, the marker `" + mtbAssistantMarker + "`, and its second answer. The task's\n" +
			"`rubric.md` tells the judge that is what it is reading.\n\n" +
			"This is not a presentation choice. The estimand is the annotators': a person\n" +
			"comparing two systems on a second turn read each system's *whole* two-turn\n" +
			"conversation, and the follow-up questions are usually critiques of the first\n" +
			"answer — \"Take a moment to evaluate and critique your own response\". A judge\n" +
			"shown only the two user turns would be ranking three critiques of three answers\n" +
			"it has never seen, each critiquing something different, on half the corpus. It\n" +
			"would be measuring a different quantity from the one the gold label measures,\n" +
			"and `human_kappa` and the verdict rest on those being the same quantity.\n\n" +
			"The assistant turn is inside the candidate rather than in the shared context\n" +
			"because it is not shared: each system answered the first question its own way,\n" +
			"so there is no first answer that belongs to the item rather than to a candidate.",
		"`winner: tie` in the source folds into a drawn pair. The source draws no\n" +
			"distinction between \"equally good\" and \"equally bad\", and neither does a\n" +
			"majority.",
	}
}
