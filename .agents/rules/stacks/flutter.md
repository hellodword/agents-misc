---
id: stack.flutter
kind: stack
triggers:
  - "Flutter"
  - "Dart"
  - "widget test"
  - "flutter analyze"
  - "mobile UI"
summary: Apply Flutter defaults for formatting, analysis, state management, and tests.
companions: []
---

# Flutter Rules

- Follow existing project conventions first.
- Format with `dart format`.
- Analyze with `flutter analyze`.
- Test with `flutter test`.
- Use `analysis_options.yaml`.
- Do not include web support by default.
- Do not run Android emulators by default.
- Do not edit Android/iOS/macOS/Windows/Linux platform folders unless the task requires platform integration.
- Do not add signing, release, store, Firebase, deployment, or cloud config unless explicitly requested.
- Commit `pubspec.lock` by default.

## State Management

- Use the project's existing state management approach.
- If the project has no state management convention, use `setState` for simple local widget state.
- Do not introduce Riverpod, BLoC, GetX, Provider, Redux, or similar libraries unless app complexity justifies it or the user asks.

## Testing

- Prefer widget tests for UI behavior.
- Prefer unit tests for pure logic.
- Integration tests require explicit need and a supported target environment.
- Do not assume emulator, KVM, or device access in devcontainers.
