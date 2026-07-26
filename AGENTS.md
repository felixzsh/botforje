# Global Context

## Project Identity

BotForje is a long-running application designed for long-term evolution.

The project prioritizes maintainability, clarity, and operational reliability over rapid feature delivery.

Every change should make the codebase easier to understand, extend, and operate in the future.

---

## Coding Principles

* Favor simple designs over sophisticated architectures
* Model the domain first, infrastructure second
* Clear responsibilities over multifunction components
* Explicit dependencies over hidden relationships
* Small APIs over large abstractions
* Composition over hierarchy
* Flat structures over deep hierarchies
* Local reasoning over global knowledge
* Readability over cleverness
* Consistency over personal preference
* Evolve abstractions from real needs, not predictions
* Optimize for maintainability before optimization for performance
* Prefer boring and proven solutions
* Keep concurrency contained and intentional
* Minimize shared mutable state
* Prefer the standard library unless a dependency provides significant value
* Package boundaries should remain clear and easy to explain

---

## Structure Hints (TBD)

---

## Commands (TBD)

---

## Never Do

* Introduce abstractions without a demonstrated need
* Create catch-all packages (`utils`, `helpers`, `common`, `misc`)
* Mix domain logic with transport, persistence, or framework concerns
* Hide dependencies behind globals or service locators
* Add dependencies without explicit approval
* Optimize before identifying a real bottleneck
* Remove files without explicit request
* Perform unrequested refactors
* Perform unrequested fixes
* Interact with git or GitHub

---

## Always Do

* Make focused, atomic changes
* Follow existing project conventions
* Treat the codebase as the source of truth
* Preserve clear ownership of responsibilities
* Prefer explicitness over magic
* Keep public APIs small and intentional
* Leave the codebase at least as maintainable as you found it
* Discuss architectural changes before implementing them
* Keep code, logs, comments, and documentation in English
