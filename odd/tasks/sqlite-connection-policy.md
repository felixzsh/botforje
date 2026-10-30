# SQLite connection policy

Feature branch `fix/sqlite-connection-policy`. Evidence and measurements:
`odd/tasks/multi-bot-load-harness.md` and
`docs/channels/load-testing-plan.md` section 6.

## Specs

S1 — Bounded connection pool and a longer busy timeout in the SQLite adapter.
User authorization: "si, arranca" to the concrete plan of changing
`busy_timeout` from 5 s to 30 s and capping the pool at 8 connections, with
tests.

S2 — The canonical ADR must state the connection policy. User authorization:
"si, arranca" to "settings + tests + ADR".

S3 — The load harness must expose the production pacing knobs so a run can use
the operator's real configuration. User authorization: "si, arranca" to "y
después el pacing del harness".

S4 — The accepted per-instance target is 300 stable bots; 500 is a measured
stretch, not a promise. User request: "okey admito 300". Original expectation
for context: "un bot conteste cada mensaje que le llega aproximadamente en
digmaos a lo mucho unos 6 segundos entonces po rbot 10 mensajes por minuto".

## Tasks

| ID | Specs | Route | Commit | State |
| --- | --- | --- | --- | --- |
| T1 | S1, S2 | inline | 3dd05d3 | done |
| T2 | S3 | inline | | pending |
| T3 | S4 | inline | | pending |

T1 — RED tests for the timeout and the pool bound, then the two settings and the
ADR 0006 update. Its own commit: the Risk line names a high-risk item
(concurrency).

T2 — Pacing knobs in the load harness, documented in the plan document; test-only
code, separate commit.

T3 — Measure 300 and 500 bots with pacing, repeat each point, and record the
per-message latency against the 6 s budget.

## Log

L1 — User request, verbatim: "si, arranca". Authorizes the settings change with
tests and ADR, then the harness pacing.

L2 — User request, verbatim: "okey admito 300, pero entonces que hay que
implementar especificamente a que te refieres con los settings ?". Accepts 300
bots per instance as the target and asks for the concrete implementation.

L3 — Measured basis for T1, from the harness runs: at 500 bots and 83 offered
turns/s the current code failed 1333 of 2500 turns and saturated all 8 cores;
the same load with `busy_timeout=30s` and `SetMaxOpenConns(8)` completed 2500 of
2500 with zero duplicates, 85MB peak RSS, and 1.5 cores, at p95 5.6s.

L4 — Review disposition: native assess over `101e3f5..HEAD` reported
`nativeRisk: medium`, 8 paths and 187 changed lines, with `reviewDue: false`
reason `under_budget`, so no review lineage was started. The change is still
treated as high risk for concurrency: an independent read-only verifier checks
pool starvation, timeout interaction, test strength, and ADR consistency.
