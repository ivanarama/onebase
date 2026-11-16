# Portable strict SQL provenance profile

Source API (package internal/query):

```go
func CompileReportLineage(src string, opts CompileOpts) (Result, *ReportLineage, error)
func (p *ReportLineage) Outputs() []ReportLineageOutput
func (p *ReportLineage) Uses() []ReportLineageUse
func (p *ReportLineage) Matches(res Result, opts CompileOpts) bool
```

This portable slice contains the strict single-SELECT foundation only. Its parser
is identical to accepted source970; the digest wrapper is identical to wire421
and uses provenance.FormatVersion2, supplied by the parent portable2ab commit.
It does not carry report privacy policy, handlers, report context, derived/UNION
scopes, FK predicate extensions, calendar extensions, virtual tables or runtime
admission. Ordinary Compile/query.go and metadata definitions are unchanged.

Outputs returns detached expression trees with Op/Type/Parameter/LiteralDigest,
Origins and Args. Each origin retains SourceRef, FROM occurrence, canonical field,
type and every reference hop in Path. Uses returns Clause+Expression for all
JOIN/WHERE/GROUP/HAVING/ORDER dependencies. This strict version has no Scope field.
Every Use is a control/membership dependency of every output, including COUNT.
Inspecting Outputs alone cannot authorize an aggregate or result filter.

Known physical sources/JOINs, scalar expressions, CASE, limited aggregates/string
functions and literals are represented. Unsupported syntax/types, ambiguous
sources/fields/aliases, bare reference presentations, reserved output aliases,
derived/UNION/virtual sources and unknown functions return no partial receipt.
This version does not solve native DateDiff, A004/A005 passthrough or KPI VT inputs.

Matches checks typed SQL/args/projection/source metadata/params/RLS fingerprints.
Old-format receipts must be invalidated and recomputed with FormatVersion2.
Matches does not prove actor authorization, current session/roles, snapshot,
registry/module generation, trusted Query.Text origin, parameter origin kind or
permission to read/declassify protected data. A user-input value equal to a literal
remains user-input. Host frames and runtime data/control taint are separate proofs.

These APIs are server inspection only. Result SQL/args, literal fingerprints and
inspection JSON must not become client/preset/snapshot evidence. Keep the immutable
receipt private; detached views and zero/deserialized handles cannot mint authority.
No execution/SQL reads, mask bypass, final filter schema or DSL variable is enabled.
The host must still enforce object/field/RLS/output policy before any execution and
bind every input, source, operation and output to a successful host trace.

Publication is a portable candidate: independently accepted constituent blobs do
not automatically accept this new base/carry exact. One test adapter uses ordinary
role masking available on base main instead of importing full report privacy policy.
New compile/test runs wait for the assigned Go allocation lane and fresh disk barrier.
ENG06/full ENG16 runtime/native/SDK compatibility remains HOLD.
