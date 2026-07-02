# Architecture Rules

- Prefer a modular monolith for solo full-stack projects unless distribution is explicitly required.
- Start from domain concepts, invariants, data ownership, and user flows.
- Define schemas, protocols, API contracts, and FFI contracts before implementation when boundaries cross modules, processes, storage, FFI, or network.
- Keep boundaries explicit:
  - UI/presentation;
  - application/use cases;
  - domain rules;
  - infrastructure/adapters;
  - persistence.
- Do not introduce microservices, queues, event buses, plugin systems, or distributed coordination by default.
- Use ADRs only for durable decisions with meaningful trade-offs.
- Prefer boring, debuggable interfaces over clever abstractions.
- Avoid circular dependencies.
- Keep data migrations explicit and reversible when practical.
- Design for local reproducibility first.
- Keep the first working vertical slice small.
