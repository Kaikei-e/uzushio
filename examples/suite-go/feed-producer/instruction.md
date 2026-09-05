Both tests hang and then fail. `Stream` sends every item on the channel it
returns, but nothing ever closes that channel, so a `for range` over it
waits forever after the last item.

Closing is the sending side's job — the reader cannot do it, and a second
close panics. Make the goroutine that sends the items leave the channel
closed once it has sent them all, including when there were no items at
all. Fix `Stream` in `stream.go`. Do not change the test.
