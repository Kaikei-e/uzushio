Each candidate is a **two-turn transcript**, not a single answer.

It holds the assistant's answer to the question above, then the marker `[user]` and the follow-up question every candidate was asked, then
the marker `[assistant]` and the assistant's answer to it.

Judge the transcript as a whole: the follow-up answer usually depends on the
first one, and the candidates' first answers differ. The markers are
structure, not content — do not reward or penalise a candidate for them.
