# Checking Markdown conflict resolution

The existing `-kind entries` and `-kind plans` modes retain their strict diff3
contracts. A nonempty conflicting historical base still requires a decision.

`-kind main-additions` is a separate, narrower current-parent check:

```sh
go run ./tools/mergecheck -kind main-additions -base old.md -ours source.md -theirs main.md -result CHANGELOG.md
```

It requires the result to equal the **complete source file byte for byte**, a
nonempty current main, at least one addition, and every current main line
(including blank and repeated lines and final newline) preserved in order.
Consequently only insertions relative to the whole current main are accepted.
Deletion, replacement, reordering, dropped occurrences and edits to the result
fail. The historical base is read and validated but does not define this mode's
predicate: both current parents may already have replaced old placeholder text.
This structural proof does not decide whether newly added statements are true.

`-resolve` can write that exact source only into a file containing a complete
unresolved conflict. It refuses an already resolved file and leaves rejected
inputs untouched. This mode does not grant permission to change a branch, widen
the canonical MERGE allowlist, bypass OWNER or skip independent REVIEW/CI8.
