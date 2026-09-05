Both tests fail. `Tier` searches a sorted list of tier minimums for the
last tier whose minimum is at or below the quantity. The search stops one
step too early: when the range narrows to a single candidate that candidate
is never examined, so quantities that belong to the highest tier land in
the one below it, and a one-tier list never matches at all.

Fix the search in `tier.go` so that every quantity finds its tier, a
quantity below the first minimum still answers -1, and the search still
halves the range rather than walking it. Do not change the test.
