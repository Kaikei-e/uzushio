# Attribution

The items in this directory are derived from **`lmsys/mt_bench_human_judgments`**,
published on the Hugging Face Hub at
<https://huggingface.co/datasets/lmsys/mt_bench_human_judgments>.

The dataset card declares `license: cc-by-4.0`, so the source and this
derivation are both under the **Creative Commons Attribution 4.0
International** licence: <https://creativecommons.org/licenses/by/4.0/>.

The dataset accompanies:

> Lianmin Zheng, Wei-Lin Chiang, Ying Sheng, Siyuan Zhuang, Zhanghao Wu,
> Yonghao Zhuang, Zi Lin, Zhuohan Li, Dacheng Li, Eric P. Xing, Hao Zhang,
> Joseph E. Gonzalez and Ion Stoica.
> *Judging LLM-as-a-Judge with MT-Bench and Chatbot Arena.*
> arXiv:2306.05685.

## What was taken, and what was changed

Taken: the human `winner` verdicts of the `human` split, the answers the
compared models gave, and the questions they answered.

Changed: the pairwise verdicts were aggregated into three-way labels, and
the items were sampled. `DERIVATION.md` states exactly how, with the counts.
CC BY 4.0 asks that changes be indicated; that file is the indication.

Not changed: every `candidates/*.txt` file holds one model's answer as the
dataset carries it, byte for byte apart from a trailing newline. They are
outputs of the models named in each item's `gold.json`, produced in 2023,
and this repository does not edit them — a corpus whose answers have been
tidied is not the corpus the human labels were collected on.
