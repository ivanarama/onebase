# pipelinehealth: optional GraphQL cost proof

The normal command and its existing queue, identity, proof, selection, retry and
HTTP bounds are unchanged. An explicit caller may request an additional
`source-graphql-cost-v1` receipt for a live full GraphQL JSON observation:

```sh
pipelinehealth -json -transport graphql -technical-handoff \
  -source-graphql-budget 100 \
  -source-graphql-nonce 0123456789abcdef0123456789abcdef
```

Use a fresh 32-character lowercase hex nonce for every invocation. Both flags
are required; the integer allowance is finite and positive. The opt-in cannot
be combined with REST, offline fixtures, `-routing-inspect`, or the separate
`-pr-identity` view. It does not alter GitHub, create tasks, change routing
policy, or grant Source/Native/mutation admission.

## Accounting contract

One meter belongs to the whole invocation, including the ordinary snapshot,
the separately constructed typed-routing client, pagination and queue-churn
restarts. The central physical transport serializes each preview/data pair
under that shared meter. It does not infer point cost from HTTP request count
or account-wide `used`/`remaining` differences.

Before each unknown-price preview, the meter escrows the ENTIRE remaining
allowance. It adds an aliased `rateLimit(dryRun: true) { cost }` to the exact
closed canonical query and sends the same variables. Only an error-free,
strictly decoded, positive integer server quote within escrow settles that
preview charge. Its remaining unused escrow is released; that is not regional
quota credit and does not reduce already committed charges. There is no
assumption that a preview costs 0 or 1.

Before the data request the meter separately reserves that exact quoted cost.
It requests `rateLimit { cost }` and an aliased `viewer { login }` in the same
canonical data query. A clean actual cost at most the quote settles the actual
charge; authenticated viewer must equal the configured trusted account. The
instrumentation fields are removed before the existing canonical reader sees
data. Query/proof/identity/selection logic is unchanged.

Missing, duplicate, malformed, zero, noninteger or excessive costs; a nonempty
or malformed `errors` field (including `null`); credential mismatch; command
failure; truncation; or timeout permanently poisons this cost observation.
Its reservation is not refunded. No further physical request or ordinary
gateway retry can recover a successful cost proof from that invocation.
Normal non-opt-in gateway retries remain unchanged. The existing 10000 physical
query bound also includes previews in this optional mode; response/stderr/time
bounds retain their existing values.
The optional strict JSON reader independently limits nesting to64 and total
JSON values to65536, while preserving the64MiB byte ceiling for large strings.
The separate routing/identity parser's8MiB contract remains unchanged.

GitHub documents [the dry-run query and cost field](https://docs.github.com/en/graphql/reference/meta)
and [rate limits, including possible additional charges after timeouts](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api).
This protocol proves cost only for a successful complete observation. It
cannot promise a physical charge ceiling for a failed or unknown server request.

## Receipt and consumer checks

Successful full output adds `source_graphql_usage`, described by
[source_graphql_cost.schema.json](source_graphql_cost.schema.json).
`complete:true` requires a complete canonical report, unchanged contract bytes,
and all physical costs proved. Red health findings can still be a complete
observation and retain the existing exit code 1. A technical/incomplete cost
failure emits ONLY the receipt, with no partial queue or routing allowlist.
Only a clean, certain `allowance_exhausted` refusal exits75 (EX_TEMPFAIL), with
zero unproved attempts and consistent known costs/counters. It is an availability
observation, never Source PASS. Unknown cost, malformed receipts, gateway/timeouts,
physical-query exhaustion and contract/provenance errors retain technical exit2.
Invalid options can fail before any receipt is issued.

In addition to the schema, consumers must check these semantic equalities:

- protocol, nonce, allowance, repository, trusted account and SHA256 of the
  exact requested contract match this actual invocation;
- `physical_attempts == preview_attempts + data_attempts`;
- for success, `preview_attempts == data_attempts > 0`, `unproved_attempts == 0`,
  `uncertain_cost == false`, and
  `charged_upper_bound == observed_actual_cost <= allowance`;
- the requested full report and all independent Source/proof/admission gates
  remain valid. A cost receipt alone grants none of those permissions.

On failed output, `charged_upper_bound` is retained local accounting escrow,
not proof of a server-side upper bound. A consumer must never treat a missing,
old, uncertain or incomplete receipt as a zero-cost successful refresh. It must
independently pin the immutable invoked executable and command/provenance.
Nonce binding does not authenticate an arbitrary executable, `GH_EXE`, or
locally forged receipt. Existing operator/credential trust boundaries apply.

The default allowance in an external profile is a separate operator policy;
this tool does not increase it or relax any configured reservation or floor.
