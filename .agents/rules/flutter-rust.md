# Flutter and Rust Integration

- Organize Flutter code around View/ViewModel, Repository, and Service layers, isolating platform channels and Rust FFI behind repositories/services.
- Reserve `setState` for widget-local presentation state; default to SDK `ChangeNotifier` and `Listenable` for view-model state unless project complexity or user choice selects another library.
- For `flutter_rust_bridge` builds: use stable Cargokit in standard greenfield setups, or inject prebuilt target dynamic libraries (`jniLibs`, frameworks, `LD_LIBRARY_PATH`) directly when the project uses hermetic Nix or offline build graphs without build-time Cargo side effects; use Native Assets only when adopted and supported by the pinned SDK.
- Pin compatible Dart, Rust, codegen, and macro versions in project lockfiles. Track required generated bridge sources under commit policy and validate both Rust and Flutter consumers.
- Point `flutter_rust_bridge` `rust_input` at a dedicated thin FFI facade module or crate, keep FFI types explicit, and validate nullability, ownership, threading, cancellation, and error mapping at the boundary. Do not add web targets, simulators, signing, or cloud/release config unrequested.
