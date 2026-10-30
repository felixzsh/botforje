# Multi-bot integration and capacity testing plan

Status: planned, not implemented. This document defines follow-up testing on
one Botflota process with many synthetic messaging accounts. It does not
claim a measured capacity or authorize runtime contract changes.

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

The existing fake retains every outgoing message and processing receipt, and
its observation methods copy their full histories. Its inbound injection also
blocks without cancellation. Before sustained-load use, provide bounded,
observable instrumentation and cancellable injection without breaking the
existing conformance tests. Otherwise the harness can dominate memory and
shutdown behavior.

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

Expose these controls in the future test runner; their spelling and invocation
will be documented with its implementation, not presented as existing commands.

| Control | Purpose |
| --- | --- |
| Bot count | Independently running accounts in one fleet. |
| Scope count and bot distribution | Make isolation topology explicit. |
| Active chats per bot | Separate account overhead from conversation work. |
| Offered messages per second per bot | Define aggregate load as bot count times per-bot rate. |
| Duration, warm-up, and drain deadline | Separate steady state from startup and completion. |
| Traffic pattern and seed | Reproduce steady traffic and synchronized bursts. |
| Payload size | Keep message-memory and storage costs explicit. |
| Simulated send latency and failure schedule | Exercise slower channels and controlled failures. |

Record the graph, reply count per input, and effective outbox pacing with each
run. Keep production pacing for the baseline. Any separately configured pacing
experiment must be labeled and must not be compared as the same workload.

Generate traffic on a scheduled, open-loop basis: do not wait for a reply before
offering the next input. Record scheduling lag and missed offers when the
injector cannot sustain the requested rate. Bound generator concurrency and
buffers; never create an unbounded goroutine or queue per offered message.
A generator bottleneck makes the intended load unproven, not supported.

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

Offer optional CPU and heap profiles and, when diagnosing contention, Go block
or mutex profiles. Keep profile runs separate from baseline measurements and
label their overhead. Profiling is diagnostic, not a prerequisite for every run.

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

## 6. Implementation sequence and acceptance

All items below are future work; no benchmark or capacity result exists yet.

- [ ] Add cancellation-safe synthetic driving and bounded load observations;
  existing adapter conformance tests continue passing.
- [ ] Implement the functional scenarios with exact correlated assertions,
  isolated databases, deadlines, and race-detector coverage.
- [ ] Add an opt-in configurable load runner with bounded open-loop generation,
  explicit startup/warm-up/measurement/drain phases, and honest overload accounting.
- [ ] Add summary and machine-readable evidence plus optional Go profiles;
  distinguish runtime cost from harness overhead.
- [ ] Document runnable examples and Make targets alongside their implementation;
  heavy stress runs remain opt-in rather than part of ordinary `make check`.
- [ ] Execute and record a baseline and incremental sweep on named hardware;
  report limitations and candidate operating envelopes, not unsupported maxima.

Next step: implement the synthetic-driving prerequisite and functional suite
before using throughput or profiling output to draw capacity conclusions.
