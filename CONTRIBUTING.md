# Contributing

Working principles for humans and agents. `AGENTS.md` holds the operational
rules; this file explains the reasoning behind them.

## Design principles

- Favor simple designs over sophisticated architectures.
- Model the domain first, infrastructure second.
- Clear responsibilities over multifunction components.
- Explicit dependencies over hidden relationships.
- Small APIs over large abstractions.
- Composition over hierarchy.
- Flat structures over deep hierarchies.
- Local reasoning over global knowledge.
- Readability over cleverness.
- Consistency over personal preference.
- Evolve abstractions from real needs, not predictions.
- Optimize for maintainability before optimizing for performance.
- Prefer boring and proven solutions.
- Keep concurrency contained and intentional.
- Minimize shared mutable state.
- Prefer the standard library unless a dependency provides significant value.
- Package boundaries should remain clear and easy to explain.

## Always

- Make focused, atomic changes.
- Follow existing project conventions.
- Treat the codebase as the source of truth.
- Preserve clear ownership of responsibilities.
- Prefer explicitness over magic.
- Keep public APIs small and intentional.
- Leave the codebase at least as maintainable as you found it.
- Discuss architectural changes before implementing them, or record them in
  `docs/adr/` once they are settled.

## Never

- Introduce abstractions without a demonstrated need.
- Perform unrequested refactors or fixes.
- Hide dependencies behind globals or service locators.
- Add dependencies without explicit approval.
