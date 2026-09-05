The tests in `buffer_test.go` fail. `Append` is supposed to return a *new*
entry list, so an audit trail can be branched: appending twice to the same
list must give two independent lists, and the list that was passed in must
come back unchanged — including the part of its array beyond its length.
Editing an entry of the result must not edit the caller's list.

Today the second append overwrites the first. Fix `Append` in `buffer.go`
so that the list it returns never shares storage with the one it was given,
while still holding the original entries followed by the new one. Do not
change the test.
