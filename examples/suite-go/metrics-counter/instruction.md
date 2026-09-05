`TestCounterUnderConcurrentUse` fails, and the race detector reports a data
race: the verifier runs `go test -race`. Several goroutines call `Inc` while
others call `Get` on the same `Counter`, and the map behind it is read and
written with nothing coordinating them, so counts are lost and the run is
unsafe.

Make `Counter` safe to use from several goroutines at once, so that eight
goroutines incrementing five hundred times each leave a count of four
thousand and the race detector reports nothing. Both `Inc` and `Get` are
part of that. Fix `counter.go`; do not change the test.
