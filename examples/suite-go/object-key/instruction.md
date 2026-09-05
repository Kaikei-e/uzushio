`TestKey` fails. `Key` builds the key an object is stored under from its
parts. A store key is not a filename: it is always separated by "/"
whatever the host filesystem uses, so it is `path`, not `filepath`, that
describes it. Four rules follow:

- parts are joined by exactly one "/", never two;
- a part that is empty contributes nothing;
- a part of "." contributes nothing and a part of ".." steps back over the
  part before it;
- the key never begins with "/", even when a part does.

Fix `Key` in `key.go`. Do not change the test.
