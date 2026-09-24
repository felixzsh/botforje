# Contributing

Working principles for humans and agents. `AGENTS.md` holds the operational
rules and the Go mechanics (errors, context, goroutines); this file explains
the reasoning behind the design.

## Design principles

- Favor simple designs over sophisticated architectures.
- Defaults serve the simple case; configuration opts into complexity.
- Model the domain first; infrastructure adapts to it.
- Clear responsibilities over multifunction components.
- Explicit dependencies over hidden relationships (no globals or service
  locators).
- Small APIs over large abstractions.
- Local reasoning over global knowledge.
- Readability over cleverness.
- Consistency over personal preference.
- Evolve abstractions from real needs, not predictions.
- Optimize for maintainability before performance.
- Prefer boring and proven solutions.
- Prefer the standard library; add a dependency only when it clearly earns
  its place.
- Keep concurrency contained and intentional.
- Minimize shared mutable state.
- Keep package boundaries clear and easy to explain.

## Always

- Make focused, atomic changes.
- Follow existing project conventions.
- Treat the codebase as the source of truth.
- Prefer explicitness over magic.
- Leave the codebase at least as maintainable as you found it.
- Discuss architectural changes before implementing them, and record settled
  decisions (including config DSL changes) as ADRs; `AGENTS.md`, "Working
  rules", says when to stop and decide first.

## Never

- Introduce abstractions without a demonstrated need.
