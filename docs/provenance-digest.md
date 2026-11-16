# Typed provenance digest (portable internal API)

`internal/provenance` has no dependency on query/access/report/ENG06 metadata.
It computes deterministic, bounded hashes of host data. It is not a MAC,
authentication, authorization, a source classifier or proof that data is trusted.
A client-provided digest/JSON does not become host evidence by matching it.

```go
func TypedDigest(value any) ([32]byte, error)
func InputDigest(value any) ([32]byte, error)
```

TypedDigest supports exported plain structs, string-key maps, arrays/slices,
pointers to such data, scalars, time.Time and decimal.Decimal. Every nested Go
type participates. Private fields, functions/channels/unsafe host state,
non-finite floats, cycles and excess limits return ErrUnsupported with no value
in the error. No arbitrary MarshalJSON/Stringer/Valuer is invoked. time.Time and
decimal.Decimal use explicitly supported encodings; time location and decimal
coefficient/exponent remain bound. Maps are ordered canonically. Nil/empty and
typed/untyped nil remain different.

InputDigest is narrower: nil, builtin scalar types, []byte, time.Time, uuid.UUID,
decimal.Decimal and bounded scalar arrays/string-key maps. Arbitrary named
wrappers/structs/pointers are rejected; this validates a typed input domain, not
its origin. User input stays user input even if its digest equals a server
literal digest. A host must also bind slot/kind/query/module/actor/metadata/RLS
and current policy in its own private evidence.

Limits: typed snapshot depth64/nodes1048576/encoded64MiB; strict input depth32/
items65536, plus snapshot limits. Failure returns zero digest and ErrUnsupported.
Version the containing host evidence/profile; changing Go schema/type layout can
invalidate old snapshots. Use explicit exported actor/role/RLS snapshots rather
than a live context/DB object with callbacks/private caches. Keep digests private
when they could enable guessing of personal input values.

Portable source is extracted from the reviewed ENG16 typed encoder, with tests
for JSON collisions, nested types, metadata/actor/RLS shapes, method bypass,
unknown capabilities, cycles and budgets. No output-policy/PII runtime behavior
is switched by importing this package.

FormatVersion=2 fixes composite identity: every unnamed pointer/slice/array/map
component is encoded recursively, with full import path of named children,
before nil/empty and byte fast paths. Unproved instantiated generic names STOP.
InputDigest checks declared element/value/key type independently of values,
including nil/empty containers and zero-length arrays. Empty any containers stay
allowed, their present dynamic values are checked separately. V1 digests are
invalidated; host evidence must bind FormatVersion and recompute from typed data.
