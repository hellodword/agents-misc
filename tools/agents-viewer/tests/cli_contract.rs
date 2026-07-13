use std::io::{BufRead as _, BufReader, Read as _, Write as _};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

use assert_cmd::cargo::cargo_bin_cmd;
use predicates::prelude::*;
use tempfile::TempDir;

#[test]
fn help_and_version_have_stable_output() {
    cargo_bin_cmd!("agents-viewer")
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("--config"))
        .stdout(predicate::str::contains("--rebuild-index"));
    cargo_bin_cmd!("agents-viewer")
        .arg("--version")
        .assert()
        .success()
        .stdout(predicate::str::starts_with("agents-viewer "));
}

#[test]
fn port_zero_prints_one_reachable_url_line_and_lock_is_exclusive() {
    let temp = TempDir::new().unwrap();
    let source = temp.path().join("source");
    std::fs::create_dir_all(source.join("sessions")).unwrap();
    let rollout = source
        .join("sessions/rollout-2026-07-01T00-00-00-99999999-9999-4999-8999-999999999999.jsonl");
    let mut rollout_file = std::io::BufWriter::new(std::fs::File::create(rollout).unwrap());
    writeln!(rollout_file, r#"{{"timestamp":"2026-07-01T00:00:00Z","type":"session_meta","payload":{{"id":"99999999-9999-4999-8999-999999999999"}}}}"#).unwrap();
    for index in 0..25_000 {
        writeln!(rollout_file, r#"{{"type":"event_msg","payload":{{"type":"agent_reasoning","text":"background indexing record {index}"}}}}"#).unwrap();
    }
    rollout_file.flush().unwrap();
    let data = temp.path().join("data");
    let config = temp.path().join("config.toml");
    write_config(&config, &source, &data);
    let binary = assert_cmd::cargo::cargo_bin!("agents-viewer");
    let mut child = Command::new(binary)
        .args(["--config", config.to_str().unwrap()])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdout = BufReader::new(child.stdout.take().unwrap());
    let mut line = String::new();
    stdout.read_line(&mut line).unwrap();
    let url = line.trim();
    assert!(url.starts_with("http://127.0.0.1:"));
    let authority = url.strip_prefix("http://").unwrap();
    let mut stream = None;
    for _ in 0..20 {
        if let Ok(value) = std::net::TcpStream::connect(authority) {
            stream = Some(value);
            break;
        }
        std::thread::sleep(Duration::from_millis(25));
    }
    let mut stream = stream.expect("printed URL must accept connections");
    stream
        .write_all(
            format!(
                "GET /api/v1/events HTTP/1.1\r\nHost: {authority}\r\nAccept: text/event-stream\r\nConnection: keep-alive\r\n\r\n"
            )
            .as_bytes(),
        )
        .unwrap();
    let second = Command::new(binary)
        .args(["--config", config.to_str().unwrap()])
        .output()
        .unwrap();
    assert_eq!(second.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&second.stderr).contains("already locked"));
    assert!(temp.path().join("schema.json").is_file());
    #[cfg(unix)]
    {
        let status = Command::new("kill")
            .args(["-TERM", &child.id().to_string()])
            .status()
            .unwrap();
        assert!(status.success());
        let started = Instant::now();
        let exit = loop {
            if let Some(status) = child.try_wait().unwrap() {
                break status;
            }
            if started.elapsed() > Duration::from_secs(2) {
                let _ = child.kill();
                panic!("agents-viewer did not exit within two seconds while SSE was connected");
            }
            std::thread::sleep(Duration::from_millis(20));
        };
        assert_eq!(exit.code(), Some(0));
        let mut remainder = String::new();
        stdout.read_to_string(&mut remainder).unwrap();
        assert!(remainder.is_empty(), "stdout must contain exactly one line");
        let mut stderr = String::new();
        child
            .stderr
            .take()
            .unwrap()
            .read_to_string(&mut stderr)
            .unwrap();
        assert!(!stderr.contains("graceful shutdown exceeded"), "{stderr}");
    }
}

#[cfg(unix)]
#[test]
fn warm_start_keeps_background_reconcile_off_stderr() {
    let temp = TempDir::new().unwrap();
    let source = temp.path().join("source");
    std::fs::create_dir_all(source.join("sessions")).unwrap();
    std::fs::write(
        source.join("sessions/rollout-2026-07-01T00-00-00-88888888-8888-4888-8888-888888888888.jsonl"),
        b"{\"timestamp\":\"2026-07-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"88888888-8888-4888-8888-888888888888\"}}\n{\"timestamp\":\"2026-07-01T00:00:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\",\"message\":\"warm start fixture\"}}\n",
    )
    .unwrap();
    let data = temp.path().join("data");
    let config = temp.path().join("config.toml");
    write_config(&config, &source, &data);
    let binary = assert_cmd::cargo::cargo_bin!("agents-viewer");

    let mut first = Command::new(binary)
        .args(["--config", config.to_str().unwrap()])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut first_stdout = BufReader::new(first.stdout.take().unwrap());
    let mut url = String::new();
    first_stdout.read_line(&mut url).unwrap();
    assert!(url.starts_with("http://127.0.0.1:"));
    let mut first_stderr = BufReader::new(first.stderr.take().unwrap());
    let mut line = String::new();
    loop {
        line.clear();
        assert_ne!(first_stderr.read_line(&mut line).unwrap(), 0);
        if line.contains("index ready") {
            break;
        }
    }
    std::thread::sleep(Duration::from_millis(100));
    stop_process(&mut first);
    assert_eq!(first.wait().unwrap().code(), Some(0));

    let mut second = Command::new(binary)
        .args(["--config", config.to_str().unwrap()])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut second_stdout = BufReader::new(second.stdout.take().unwrap());
    url.clear();
    second_stdout.read_line(&mut url).unwrap();
    assert!(url.starts_with("http://127.0.0.1:"));
    std::thread::sleep(Duration::from_millis(500));
    stop_process(&mut second);
    let output = second.wait_with_output().unwrap();
    assert_eq!(output.status.code(), Some(0));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stderr.contains("discovering sessions"), "{stderr}");
    assert!(!stderr.contains("indexing "), "{stderr}");
    assert!(!stderr.contains("index ready"), "{stderr}");
}

#[cfg(unix)]
fn stop_process(child: &mut std::process::Child) {
    let status = Command::new("kill")
        .args(["-TERM", &child.id().to_string()])
        .status()
        .unwrap();
    assert!(status.success());
}

fn write_config(path: &std::path::Path, source: &std::path::Path, data: &std::path::Path) {
    let mut options = std::fs::OpenOptions::new();
    options.create_new(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt as _;
        options.mode(0o600);
    }
    let mut file = options.open(path).unwrap();
    writeln!(
        file,
        "source_dir = {:?}\ndata_dir = {:?}\ninitial_index_days = -1\nlisten = \"127.0.0.1:0\"\nmax_event_bytes = \"32MiB\"\nlog_level = \"warn\"",
        source.to_string_lossy(),
        data.to_string_lossy(),
    )
    .unwrap();
}
