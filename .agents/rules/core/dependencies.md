---
id: core.dependencies
kind: core
triggers:
  - "dependency"
  - "package"
  - "library"
  - "license risk"
  - "supply chain"
  - "new dependency"
summary: Add dependencies only when justified and through project-local tooling.
load_with: []
---

# Dependency Rules

Prefer no new dependency when the standard library or an existing project dependency is enough.

Add a dependency only when it clearly improves correctness, security, maintainability, interoperability, or implementation scope.

Prefer dependencies with:

- compatible license;
- active maintenance;
- recent stable releases;
- clear documentation;
- stable public API;
- strong ecosystem adoption;
- evidence of production use;
- reputable audit or mature security posture;
- low transitive dependency count;
- low unresolved security risk;
- compatibility with Nix/devcontainer builds;
- no surprise telemetry;
- no unnecessary postinstall scripts;
- no arbitrary binary downloads;
- no global installation requirement.

Open-source impact, reputable audits, and broad use by well-known organizations are positive signals, but they do not override security, maintenance, license, or fit.

Do not add a dependency for trivial helpers, one-off formatting, or code that can be safely written in a few clear lines.
