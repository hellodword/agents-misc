# Flutter and Rust Integration

- Follow Flutter's View/ViewModel, Repository, and Service architecture. Keep platform and Rust integration behind services/repositories.
- Use `setState` only for widget-local presentation state.
- Use SDK `ChangeNotifier` and `Listenable` for business-facing view-model state by default. Add a third-party state library only when the user selects it or demonstrated complexity requires it.
- Use the stable Cargokit integration for flutter_rust_bridge by default. Use Native Assets only when the project already adopts it or the user selects it and the chosen Flutter/Dart SDK supports it.
- Keep Dart, Rust, runtime, code generator, and macro versions compatible and pinned by the project toolchain/lockfiles.
- Commit generated bridge files required by Flutter or Rust builds. Document the generation command, validate both consumers, and confirm a second generation has no diff.
- Keep FFI types explicit and validate nullability, ownership, threading, cancellation, and error mapping at the boundary.
- Use `dart format`, Flutter widget tests for UI state, Rust tests for core logic, and integration tests for the bridge.
- Do not add web support, simulators, signing, release, deployment, or cloud configuration without an explicit requirement.
