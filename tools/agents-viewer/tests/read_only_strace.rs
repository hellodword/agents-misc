#![cfg(target_os = "linux")]

use std::fs::{OpenOptions, read_dir, read_to_string};
use std::io::{Read as _, Write as _};
use std::os::unix::fs::{OpenOptionsExt as _, PermissionsExt as _};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

use tempfile::TempDir;

#[test]
#[ignore = "Linux strace syscall acceptance gate"]
fn viewer_never_mutates_the_source_tree() {
    assert!(
        Command::new("strace").arg("--version").output().is_ok(),
        "strace is required"
    );
    let temp = TempDir::new().unwrap();
    let source = temp.path().join("source");
    let sessions = source.join("sessions/2025/01/02");
    let data = temp.path().join("data");
    std::fs::create_dir_all(&sessions).unwrap();
    std::fs::create_dir_all(&data).unwrap();
    std::fs::set_permissions(&data, std::fs::Permissions::from_mode(0o700)).unwrap();
    std::fs::copy(
        concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/fixtures/rollouts/v0_120.jsonl"
        ),
        sessions.join("rollout.jsonl"),
    )
    .unwrap();
    make_read_only(&source);
    let _restore = WritableOnDrop(source.clone());

    let config = data.join("config.toml");
    let mut config_file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&config)
        .unwrap();
    writeln!(
        config_file,
        "source_dir = {:?}\ndata_dir = {:?}\ninitial_index_days = -1\nlisten = \"127.0.0.1:0\"\nmax_event_bytes = \"32MiB\"\nlog_level = \"warn\"",
        source.to_string_lossy(),
        data.to_string_lossy(),
    )
    .unwrap();
    drop(config_file);

    let trace = temp.path().join("trace");
    let stdout_path = temp.path().join("stdout");
    let stderr_path = temp.path().join("stderr");
    let stdout = std::fs::File::create(&stdout_path).unwrap();
    let stderr = std::fs::File::create(&stderr_path).unwrap();
    let mut traced = Command::new("strace")
        .args(["-ff", "-e", "trace=file", "-o"])
        .arg(&trace)
        .arg(env!("CARGO_BIN_EXE_agents-viewer"))
        .args(["--config"])
        .arg(&config)
        .stdout(Stdio::from(stdout))
        .stderr(Stdio::from(stderr))
        .spawn()
        .unwrap();

    let deadline = Instant::now() + Duration::from_secs(15);
    let viewer_pid = loop {
        if let Some(status) = traced.try_wait().unwrap() {
            panic!(
                "viewer exited before readiness ({status}); stderr: {}",
                read_to_string(&stderr_path).unwrap_or_default()
            );
        }
        if read_to_string(&stdout_path).is_ok_and(|output| output.contains("http://")) {
            let children = read_to_string(format!("/proc/{0}/task/{0}/children", traced.id()))
                .unwrap_or_default();
            if let Some(pid) = children.split_whitespace().next() {
                break pid.parse::<i32>().unwrap();
            }
        }
        assert!(Instant::now() < deadline, "viewer did not become ready");
        std::thread::sleep(Duration::from_millis(50));
    };
    assert_eq!(unsafe { libc::kill(viewer_pid, libc::SIGTERM) }, 0);
    let status = traced.wait().unwrap();
    assert!(status.success(), "strace/viewer failed: {status}");

    let source_text = source.to_string_lossy();
    let data_text = data.to_string_lossy();
    let temp_text = temp.path().to_string_lossy();
    for entry in read_dir(temp.path()).unwrap().flatten() {
        if !entry.file_name().to_string_lossy().starts_with("trace.") {
            continue;
        }
        let mut contents = String::new();
        std::fs::File::open(entry.path())
            .unwrap()
            .read_to_string(&mut contents)
            .unwrap();
        for line in contents.lines().filter(|line| is_mutation(line)) {
            assert!(
                !line.contains(source_text.as_ref()),
                "mutating syscall targeted source: {line}"
            );
            if line.contains(temp_text.as_ref()) {
                assert!(
                    line.contains(data_text.as_ref()),
                    "write under test root escaped data_dir: {line}"
                );
            }
        }
    }
}

fn is_mutation(line: &str) -> bool {
    ["O_WRONLY", "O_RDWR", "O_CREAT", "O_TRUNC"]
        .iter()
        .any(|flag| line.contains(flag))
        || [
            "rename(",
            "renameat(",
            "renameat2(",
            "unlink(",
            "unlinkat(",
            "mkdir(",
            "mkdirat(",
            "rmdir(",
            "chmod(",
            "fchmodat(",
            "chown(",
        ]
        .iter()
        .any(|call| line.contains(call))
}

fn make_read_only(path: &std::path::Path) {
    for entry in walkdir::WalkDir::new(path).contents_first(true) {
        let entry = entry.unwrap();
        let mode = if entry.file_type().is_dir() {
            0o555
        } else {
            0o444
        };
        std::fs::set_permissions(entry.path(), std::fs::Permissions::from_mode(mode)).unwrap();
    }
}

struct WritableOnDrop(std::path::PathBuf);

impl Drop for WritableOnDrop {
    fn drop(&mut self) {
        for entry in walkdir::WalkDir::new(&self.0).into_iter().flatten() {
            let mode = if entry.file_type().is_dir() {
                0o700
            } else {
                0o600
            };
            let _ = std::fs::set_permissions(entry.path(), std::fs::Permissions::from_mode(mode));
        }
    }
}
