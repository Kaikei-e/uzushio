`TestWaitReturnsWhenTheCallerGivesUp` fails: `Wait` blocks on the work and
never looks at the context, so a caller that cancels, or whose deadline
runs out, is never answered.

Wait for whichever comes first. If the work finishes first, return nil. If
the context is done first, return the context's own error, so that a
cancellation and an expired deadline can be told apart. Fix `Wait` in
`wait.go`. Do not change the test.
