# Multi-bot integration and capacity testing plan

Status: bounded synthetic driving, the functional integration suite, and the
opt-in capacity runner are implemented, and section 6 records one sweep on the
development machine. That record is descriptive evidence, not a published
capacity claim. This
document defines testing on one Botflota process with many synthetic messaging
accounts and does not authorize runtime contract changes.

## Outcome

Exercise the real fleet, engine, and SQLite stores through a simulated channel,
then increase the number of bots and offered traffic to find a reproducible
operating envelope on a specified machine.

One adapter implementation supplies many independent bot instances; hundreds
of adapter implementations are not needed. Functional correctness and capacity
are separate results: a fast run with incorrect replies is not successful.

## Existing foundation and boundaries

- Reuse `adapters/testchannel` where appropriate. Its conformance suite already
  exercises the adapter definition, stores, fleet, engine, and API together.
- Keep the real routing, persistence, filters, and durable outbox in the tested
  path. Start with deterministic graphs and prescribed replies, without LLMs
  or external HTTP services.
- Use isolated temporary databases and synthetic identities, never real
  accounts, credentials, or operator data.
- Keep the load harness outside the production command surface. Prefer Go's
  standard testing and profiling tools and existing dependencies.
- Do not change shared ports, storage contracts, pacing defaults, or resource
  policy merely to make a benchmark faster. Findings may propose separate work.

The synthetic adapter now keeps bounded observations: each bot caps how many
sent messages and processing receipts it retains, counts every delivery even
when its sample is trimmed, and reports sends through a per-send observer. Its
inbound injection honors cancellation, so shutdown under load does not hang.
The existing conformance tests still pass with those defaults.

[Channel preparation](plan.md) owns the architectural gates. Its pending
WhatsApp operational checklist remains independent of this synthetic work.
[Resource policy](../access-policy.md) still owns unresolved enforcement policy;
measuring capacity does not introduce quotas or fairness guarantees.

## 1. Functional integration scenarios

Use small deterministic cases for routine checks. Run the same relevant
invariants with hundreds of bot instances in an explicit stress run.

| Scenario | Observable proof |
| --- | --- |
| Concurrent bots and chats | Each input produces its expected reply and persisted state. |
| Scope and bot isolation | Reused peer/message IDs do not cross the declared namespaces. |
| Duplicate inbound delivery | Redelivery does not repeat the committed turn or its queued reply. |
| Slow or failing channel | Healthy bots progress; failures follow existing retry semantics. |
| Backpressure | Distinguish offered, admitted, completed, rejected, and pending work. |
| Shutdown under load | Injection cancels, the fleet exits by a deadline, and workers are joined. |
| Restart after committed work | State resumes; pending sends follow existing outbox semantics. |

Assert effects through adapter observations, public stores, and the existing
fleet/API interfaces, not direct SQL or private engine state. Correlate inputs
and expected outputs; aggregate counts alone cannot prove isolation or detect
a missing reply hidden by an extra one elsewhere. Do not infer transport
receipt support or recipient delivery from a successful synthetic send.

Use channels, explicit completion signals, and deadlines rather than arbitrary
sleeps for functional assertions. Concurrency scenarios also run with the race
detector, separately from capacity measurements.

## 2. Adjustable workload

The opt-in capacity runner takes these controls from its environment; [Running
it](#running-it) documents their exact spelling, defaults, and invocation.

| Control | Purpose |
| --- | --- |
| Bot count | Independently running accounts in one fleet. |
| Scope count and bot distribution | Make isolation topology explicit. |
| Active chats per bot | Separate account overhead from conversation work. |
| Offered messages per second per bot | Define aggregate load as bot count times per-bot rate. |
| Duration, warm-up, and drain deadline | Separate steady state from startup and completion. |
| Traffic pattern and seed | Reproduce steady traffic and synchronized bursts. |
| Payload size | Keep message-memory and storage costs explicit. |
| Simulated send latency and failure schedule | Follow-up, not a runner control. |

Every control except the last is implemented as an environment variable.
Simulated send latency and failures stay a documented follow-up: outbox retry
and permanent-failure semantics are already pinned by existing fleet tests, so
the runner does not duplicate them at scale with test-only machinery.

Record the graph, reply count per input, and effective outbox pacing with each
run. Keep production pacing for the baseline. Any separately configured pacing
experiment must be labeled and must not be compared as the same workload.

Generate traffic on a scheduled, open-loop basis: do not wait for a reply before
offering the next input. Record scheduling lag and missed offers when the
injector cannot sustain the requested rate. Bound generator concurrency and
buffers; never create an unbounded goroutine or queue per offered message.
A generator bottleneck makes the intended load unproven, not supported.

## Running it

The capacity run is `TestCapacity` in the test-only `loadtest` package. It is
skipped unless `BOTFLOATA_LOAD_BOTS` is set, so no ordinary test command
measures capacity. `make load` runs it with an explicit `-timeout 30m`, a
fresh run (`-count=1`), and the short revision it was built from.

| Variable | Default | Control |
| --- | --- | --- |
| `BOTFLOATA_LOAD_BOTS` | none, required | Independently running bots in one fleet. |
| `BOTFLOATA_LOAD_SCOPES` | `1` | Scopes the bots are distributed across. |
| `BOTFLOATA_LOAD_CHATS` | `1` | Active chats per bot. |
| `BOTFLOATA_LOAD_RATE` | `1` | Offered messages per second per bot. |
| `BOTFLOATA_LOAD_DURATION` | `30s` | Measurement phase length. |
| `BOTFLOATA_LOAD_WARMUP` | `5s` | Traffic before measurement, then drained. |
| `BOTFLOATA_LOAD_DRAIN` | `30s` | Deadline for unfinished work to drain. |
| `BOTFLOATA_LOAD_PAYLOAD` | `64` | Payload size in bytes. |
| `BOTFLOATA_LOAD_MAX_PENDING` | `64` | Bounded in-flight inputs per bot. |
| `BOTFLOATA_LOAD_SEED` | `1` | Seed for the generated traffic. |
| `BOTFLOATA_LOAD_INJECT_TIMEOUT` | `5s` | Deadline for one injected input. |
| `BOTFLOATA_LOAD_JSON` | none | Report path; no file is written when empty. |
| `BOTFLOATA_LOAD_REVISION` | short `HEAD` via `git` | Revision recorded in the report. |

Durations use Go syntax (`45s`, `2m`); rates, sizes, and the seed are numbers.
An absent `BOTFLOATA_LOAD_BOTS` skips the run instead of failing it. Start small
and step one control up at a time:

```sh
BOTFLOATA_LOAD_BOTS=50 BOTFLOATA_LOAD_JSON=/tmp/bots50.json make load
make load BOTFLOATA_LOAD_BOTS=100 BOTFLOATA_LOAD_CHATS=2 \
	BOTFLOATA_LOAD_DURATION=2m BOTFLOATA_LOAD_JSON=/tmp/bots100.json
```

Both forms pass the workload through: `make load` hands every
`BOTFLOATA_LOAD_*` value from the environment or the make command line to the
test binary, and an operator-set `BOTFLOATA_LOAD_REVISION` is not overridden by
the value derived from `git`. Heavy stress runs stay opt-in: `make load` is not
part of `make check`, `make test`, or `make test-race`, and no CI run enforces
a throughput or latency threshold.

Write the JSON report to a path outside the repository, such as `/tmp`, so
measurement evidence is never committed by accident. Each report carries the
revision, seed, workload, machine, phase durations, offered/admitted/completed
counts, latency percentiles, process CPU and resident memory, heap and GC
statistics, goroutines, database size, and pending-work samples.

Profiles use the standard `go test` flags rather than runner options, and stay
separate from baseline runs because they distort the numbers:

```sh
BOTFLOATA_LOAD_BOTS=100 go test ./loadtest -run TestCapacity -v \
	-timeout 30m -cpuprofile /tmp/cpu.out -memprofile /tmp/mem.out
```

`-blockprofile` and `-mutexprofile` are available the same way when diagnosing
contention. A few seconds of traffic with a payload that stays close to the
defaults is enough for a first smoke run.

## 3. Capacity experiments

1. **Idle baseline:** start bots without traffic and measure startup, idle
   memory, and goroutines. This measures account overhead, not throughput.
2. **Steady traffic:** hold chats, payload, graph, and per-bot rate fixed while
   increasing bot count. Suggested initial steps: 50, 100, 200, and 500;
   these are experiment points, not support claims.
3. **Rate sweep:** hold bot count fixed and increase offered traffic to separate
   message-processing limits from per-account overhead.
4. **Bursts and degraded channels:** introduce controlled spikes, slow sends,
   and failures; observe backlog growth and recovery after the disturbance.
5. **Sustained run near the candidate envelope:** check whether memory and
   pending work stabilize after warm-up rather than growing throughout the run.

Start with a short smoke run before expensive sweeps. Repeat candidate boundary
runs with the same configuration to expose variability. Set an explicit maximum
load and run duration; do not automatically increase until the machine fails.

## 4. Measurements and evidence

Each run produces a concise summary and machine-readable results containing:

- Revision, seed, full workload parameters, graph, pacing, and database setup.
- OS, Go version, CPU, RAM, storage, `GOMAXPROCS`, and applicable process or
  container CPU/memory limits; identify other significant machine load.
- Startup, measurement, and drain durations, reported separately.
- Scheduled and actually offered inputs, admitted work, committed turns,
  replies sent, duplicate outcomes, errors, and unfinished work.
- Achieved throughput and end-to-end p50/p95/p99 latency, measured from the
  scheduled offer to the expected synthetic outbound send. Also report
  injection lag to distinguish load generation from processing time.
- Process CPU, process resident memory, Go heap, allocation/GC statistics,
  goroutines, and database/WAL size. Heap is not a substitute for resident memory.
- Pending-work samples over time and whether work drains within the deadline.

Timeouts and unfinished work must remain visible, not disappear from latency
statistics. Keep measurement storage bounded; report the sampling or histogram
method. Disclose harness overhead because generator and runtime may share the
process and machine. Large-scale correctness checks should use bounded
correlation/accounting rather than retain every complete payload indefinitely.

CPU and heap profiles come from the standard `go test` flags, as do Go block or
mutex profiles when diagnosing contention. Keep profile runs separate from
baseline measurements and label their overhead. Profiling is diagnostic, not a
prerequisite for every run.

## 5. Interpreting reasonable capacity

There is no universal bots-per-process number. Report an operating envelope:

> On hardware H, revision R sustains N bots with C active chats per bot and
> rate M per bot, using graph G and pacing P, with latency L, memory B,
> observed errors E, and stable or growing pending work Q over duration D.

Before treating a run as acceptable for a deployment, choose its latency
budget, CPU/memory headroom, acceptable error rate, and maximum drain time.
Until those values are chosen, results are descriptive, not a production
pass/fail guarantee. Ordinary CI should enforce functional correctness, not
machine-dependent throughput or latency thresholds.

Synthetic results measure the runtime with the declared fake behavior. They do
not include a real messaging SDK's sockets, encryption, synchronization,
reconnects, upstream rate limits, or media traffic. Measure real adapters
separately before translating runtime capacity into deployment expectations.

## 6. Recorded runs on this machine (2026-10-30)

One observation, not a capacity claim: 8 vCPU, 15.6 GB RAM, Linux x86-64,
Go 1.27.3, btrfs root filesystem, the synthetic adapter, an echo graph with one
reply per input, two direct chats per bot, a 64-byte payload, production
pacing, and harness revision `0c71b13` (unchanged since). Each row is a single
run with a 5s warm-up, 30s measurement, and a 30-45s drain deadline. Variance
between repeats is not characterized yet.

`/tmp` on this machine is tmpfs, so the tmpfs rows measure an idealized
in-memory database. The btrfs rows put the database on the real filesystem
through `TMPDIR`.

| Storage | Bots | Rate | Offered | Done | Failed | Pending | Turns/s | p50 | p95 | RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| tmpfs | 50 | 1/s | 1500 | 1500 | 0 | 0 | 48.4 | 14ms | 131ms | 40MB |
| tmpfs | 100 | 0.5/s | 1500 | 1500 | 0 | 0 | 46.9 | 19ms | 280ms | 55MB |
| tmpfs | 100 | 1/s | 3000 | 2964 | 36 | 36 | 95.6 | 748ms | 10963ms | 79MB |
| tmpfs | 200 | 1/s | 5627 | 3258 | 951 | 2317 | 94.2 | 16392ms | 41189ms | 134MB |
| tmpfs | 500 | 1/s | 11179 | 517 | 3708 | 9772 | 14.8 | 31498ms | 49688ms | 994MB |
| btrfs | 50 | 1/s | 1500 | 1500 | 0 | 0 | 48.4 | 19ms | 160ms | 40MB |
| btrfs | 100 | 1/s | 3000 | 2946 | 54 | 54 | 95.0 | 2712ms | 16183ms | 80MB |

Idle overhead with 500 started bots and no traffic: 62MB RSS, 15MB heap, and
1004 goroutines, about two per bot.

Envelope for this machine and workload: roughly 50 turns/s (50 bots at one
message per second) is comfortable, with p95 under 200ms on disk and about one
CPU core. The write path plateaus near 95-100 turn commits per second regardless
of storage or bot count. Beyond that, latency grows with the queue and, with
the current 5s busy timeout, a small share of turns fails and stays replayable.

### Isolating experiments

Each change below was applied alone in a scratch copy, never committed.

| Change | Bots | Completed | Failed | Unfinished | Turns/s | p95 | Peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `_txlock=immediate` | 100 | 2951 | 49 | 49 | 95.2 | 11430ms | 78MB |
| `busy_timeout=30s` | 100 | 3000 | 0 | 0 | 96.8 | 9542ms | 80MB |
| `SetMaxOpenConns(8)` | 500 | 3565 | 0 | 6605 | 101.9 | 68627ms | 97MB |

- The immediate transaction mode changed nothing, so the busy failures are not
  a deferred-transaction upgrade.
- Raising the busy timeout removes the failures at the same offered load: the
  work fits, but the queue surfaces as multi-second latency. The 5s timeout
  decides whether overload appears as failed turns or as waiting, not how much
  work fits.
- Capping the SQLite pool at 8 connections cut peak RSS at 500 bots from 994MB
  to 97MB, removed every failed turn, and raised completed turns per second
  from 14.8 to 101.9. The 1GB peak was the unlimited connection pool, not the Go
  heap (34MB peak).

### Candidate adapter settings, not applied

A longer `busy_timeout` and a bounded connection pool are settings in
`adapters/sqlite`, not test code. They are not applied on this branch: they
change runtime behavior for every deployment and belong in their own change,
with tests and this before/after record.

## 7. Implementation sequence and acceptance

The implementation items are complete. Section 6 records one sweep; it is a
single observation per point, not a stable guarantee.

- [x] Add cancellation-safe synthetic driving and bounded load observations;
  existing adapter conformance tests continue passing.
- [x] Implement the functional scenarios with exact correlated assertions,
  isolated databases, deadlines, and race-detector coverage.
- [x] Add an opt-in configurable load runner with bounded open-loop generation,
  explicit startup/warm-up/measurement/drain phases, and honest overload
  accounting.
- [x] Add summary and machine-readable evidence plus optional Go profiles;
  distinguish runtime cost from harness overhead.
- [x] Document runnable examples and Make targets alongside their implementation;
  heavy stress runs remain opt-in rather than part of ordinary `make check`.
- [x] Execute and record a baseline and incremental sweep on named hardware;
  report limitations and candidate operating envelopes, not unsupported maxima
  (section 6, one run per point).

Next step: decide on the two candidate SQLite settings, repeat each sweep point
more than once, and measure a real adapter before translating these synthetic
numbers into deployment expectations.
