`TestSum` fails, and the verifier runs `go test -race`. The wait group's
counter is raised once for the whole loop but lowered once per goroutine,
so `Wait` stops waiting after the first result and the rest of the
goroutines drive the counter below zero.

The counter has to be raised once for each goroutine, and raised *before*
that goroutine is started — raising it inside the goroutine races with the
`Wait` that is already running. Fix `Sum` in `sum.go` so that it waits for
every goroutine and returns the finished total, with no race reported. Do
not change the test.
