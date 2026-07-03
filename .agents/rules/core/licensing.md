---
id: core.licensing
kind: core
triggers:
  - 'license'
  - 'copyright'
  - 'third-party code'
  - 'SPDX'
---

# Licensing Rules

- Default license for new non-patch projects: MIT.
- Pure patch projects follow upstream license and contribution conventions.
- Do not copy substantial third-party code into the repository without checking license compatibility.
- Prefer permissive dependencies for solo projects unless project constraints differ.
- If a dependency requires notices or has license obligations, create or update `THIRD_PARTY_NOTICES.md`.
- Create `THIRD_PARTY_NOTICES.md` only when there is an actual notice, attribution, copied-code, or license-obligation need.
- `THIRD_PARTY_NOTICES.md` does not make incompatible licenses compatible.
- Keep generated or vendored code clearly marked.
- Preserve upstream copyright headers.
- Do not add license headers broadly unless the project already uses them.
