---
id: core.time-locale-unicode
kind: core
triggers:
  - 'time'
  - 'timezone'
  - 'locale'
  - 'Unicode'
  - 'encoding'
  - 'i18n text'
summary: Handle time, locale, Unicode, and formatting consistently.
load_with: []
---

# Time, Locale, and Unicode Rules

## Time

- Store timestamps in UTC.
- Use ISO 8601 / RFC 3339 style strings at API and export boundaries unless the project defines another standard.
- Keep date-only values distinct from timestamps.
- Do not infer a timezone for date-only values without documenting it.
- Display dates and times in the user's locale/timezone when user-facing.
- Use fixed time in tests.
- Set test timezone explicitly when behavior depends on timezone.
- Avoid using local machine timezone in durable tests.

## Locale

- Default UI locales:
  - `en`
  - `zh-CN`
- Use BCP 47 style locale tags.
- Keep fallback locale behavior explicit.
- Do not concatenate translated sentence fragments when word order may differ.
- Use interpolation and pluralization mechanisms from the chosen i18n library.
- Keep numbers, currencies, dates, and relative times locale-aware.

## Unicode

- Preserve user-facing text.
- Normalize identifiers and filenames only when needed and documented.
- Prefer NFC normalization for durable identifiers when normalization is required.
- Avoid byte-length limits for user-facing text; use character or display-aware limits where appropriate.
- Treat user-controlled filenames as untrusted paths.
- For security-sensitive identifiers, consider confusable characters and casefolding rules.
- Keep search/index behavior explicit for case, accent, and width sensitivity.
