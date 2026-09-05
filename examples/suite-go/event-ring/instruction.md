`TestEventsAreOldestFirst` fails as soon as the ring has wrapped. `Events`
hands back the underlying array in the order it happens to lie in, but once
writing has wrapped round, the start of the array is the *newest* event and
the oldest one is wherever the next write will land.

Make `Events` in `ring.go` return the events oldest first in both states:
before the ring has filled, and after it has wrapped any number of times.
The ring itself must not be disturbed by reading it. Do not change the
test.
