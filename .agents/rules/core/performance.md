---
id: core.performance
kind: core
triggers:
  - "performance"
  - "benchmark"
  - "cache"
  - "latency"
  - "memory"
summary: Measure and scope performance changes before adding complexity.
companions: {}
---

# Performance Rules

- Do not optimize speculatively.
- Establish the bottleneck before large performance changes.
- Prefer simple algorithmic improvements before caching.
- Add caching only with explicit invalidation rules.
- Keep database queries bounded.
- Add pagination or streaming when lists can grow.
- Avoid loading large files fully into memory unless bounded and documented.
- For UI, keep initial load and interaction latency in mind.
- For CLI, keep startup time and predictable output in mind.
- For pure patch work, measure against the upstream accepted benchmark or narrow test target when available.
