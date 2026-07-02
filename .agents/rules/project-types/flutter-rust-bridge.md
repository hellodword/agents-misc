# Flutter + Rust Bridge Project Rules

Default stack:

- UI/client shell: Flutter.
- Native/system/performance logic: Rust.
- Bridge: flutter_rust_bridge unless the project already uses another bridge.
- State management: follow existing project; if none exists, use `setState` for simple local widget state.
- Default targets: mobile and desktop as required by the project.
- Web is not included by default.
- Android emulator is not assumed by default.
- No signing, release, store, Firebase, cloud config, or deployment by default.

Architecture:

- Keep Flutter UI, bridge boundary, and Rust core logic separate.
- Treat bridge types as contracts.
- Keep serialization explicit.
- Avoid pushing UI concerns into Rust core.
- Avoid pushing heavy domain logic into Flutter widgets.
- Document thread, lifetime, cancellation, and error mapping rules at the bridge boundary.

Validation:

- Rust unit tests for core logic.
- Flutter widget tests for UI behavior.
- Bridge tests only when the environment supports them.
- Integration tests require explicit need.
- Do not assume emulator, KVM, or physical device access in devcontainers.
