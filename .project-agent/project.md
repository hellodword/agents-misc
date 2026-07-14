# Agent Rules Kit Upstream

This repository is the upstream maintenance source for the shared `AGENTS.md` and `.agents/**` payload.

Consuming projects receive only that payload and extend it through their own `.project-agent/**`. Repository-only maintenance rules, schemas, scripts, and checks must not be referenced by the distributed payload.

Use `.project-agent/route-map.md` for maintenance routing.
