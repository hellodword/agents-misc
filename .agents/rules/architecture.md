# Architecture

- Prefer a modular monolith until measured scale, deployment independence, or ownership boundaries justify distribution.
- Organize product behavior by vertical slice while keeping UI, application, domain, infrastructure, and persistence responsibilities explicit.
- Keep domain decisions independent of transport, storage, framework, and generated-code details.
- Make dependencies point toward stable domain/application boundaries. Put integration adapters at the edge.
- Do not introduce microservices, a message bus, a plugin system, or generic extension points without a current requirement.
- Avoid hidden global state. Pass dependencies and request-scoped state explicitly.
- Extract an abstraction after repeated behavior and change pressure are visible; do not create speculative layers.
- Measure the relevant workload before performance changes. Define the metric, baseline, target, and validation.
- Add caching only with a defined key, ownership, invalidation rule, size bound, and failure behavior.
- Log enough safe context to diagnose failures, but do not add an external observability service by default.
- Preserve established boundaries unless the task explicitly changes them. Keep a refactor no broader than the behavior it enables or protects.
