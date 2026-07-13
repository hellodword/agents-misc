use std::fs::{File, OpenOptions};
use std::io::{BufReader, Seek as _, SeekFrom, Write as _};
use std::path::PathBuf;
use std::time::Instant;

use agents_viewer::index::Database;
use agents_viewer::index::coordinator::IndexCoordinator;
use agents_viewer::index::writer::spawn_writer;
use agents_viewer::rollout::{ParseContext, ParseSink, ParserOutput, RootKind, parse_rollout};
use tempfile::TempDir;

struct CountingSink(u64);

impl ParseSink for CountingSink {
    fn emit(&mut self, _output: ParserOutput) {
        self.0 = self.0.saturating_add(1);
    }
}

fn artifact_dir() -> PathBuf {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../target/agents-viewer-perf");
    std::fs::create_dir_all(&path).unwrap();
    path
}

fn context(file_name: &str, max_event_bytes: usize) -> ParseContext {
    ParseContext {
        root_kind: RootKind::Active,
        relative_path: file_name.into(),
        file_name: file_name.into(),
        modified_at_micros: 1_700_000_000_000_000,
        now_micros: 1_700_000_001_000_000,
        max_event_bytes,
    }
}

#[test]
#[ignore = "large synthetic parser memory gate"]
fn parses_80_mib_stream_without_exceeding_512_mib_rss() {
    let path = artifact_dir().join("synthetic-80mib.jsonl");
    let line = format!(
        "{{\"timestamp\":\"2026-07-01T00:00:00Z\",\"type\":\"event_msg\",\"payload\":{{\"type\":\"agent_message\",\"message\":\"{}\"}}}}\n",
        "x".repeat(4096)
    );
    let mut file = File::create(&path).unwrap();
    while file.metadata().unwrap().len() < 80 * 1024 * 1024 {
        file.write_all(line.as_bytes()).unwrap();
    }
    file.sync_all().unwrap();
    let mut sink = CountingSink(0);
    let started = Instant::now();
    let summary = parse_rollout(
        BufReader::new(File::open(&path).unwrap()),
        &context("synthetic-80mib.jsonl", 1024 * 1024),
        &mut sink,
    )
    .unwrap();
    assert!(summary.raw_record_count > 10_000);
    #[cfg(target_os = "linux")]
    assert!(peak_rss_kib() < 512 * 1024, "peak RSS exceeded 512 MiB");
    eprintln!(
        "records={} elapsed_ms={} peak_rss_kib={}",
        summary.raw_record_count,
        started.elapsed().as_millis(),
        peak_rss_kib()
    );
    std::fs::remove_file(path).unwrap();
}

#[tokio::test]
#[ignore = "2 GiB synthetic full-index no-OOM gate"]
async fn indexes_two_gibibyte_sparse_rollout_set_without_oom_or_panic() {
    let temp = TempDir::new_in(artifact_dir()).unwrap();
    let source_home = temp.path().join("source");
    let sessions = source_home.join("sessions/2026/07/01");
    std::fs::create_dir_all(&sessions).unwrap();
    for index in 0..32 {
        let path = sessions.join(format!(
            "rollout-2026-07-01T00-00-{index:02}-00000000-0000-4000-8000-{index:012}.jsonl"
        ));
        let mut file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .read(true)
            .write(true)
            .open(path)
            .unwrap();
        file.set_len(64 * 1024 * 1024).unwrap();
        file.seek(SeekFrom::End(-1)).unwrap();
        file.write_all(b"\n").unwrap();
    }
    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "performance-source")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots,
        1024 * 1024,
        agents_viewer::index::InitialIndexPolicy::all(),
    );
    let started = Instant::now();
    let report = coordinator.reconcile().await.unwrap();
    assert_eq!(report.generation, 1);
    assert_eq!(report.discovered_files, 32);
    assert_eq!(report.discovered_bytes, 2 * 1024 * 1024 * 1024);
    assert_eq!(report.indexed_files, 32);
    assert_eq!(report.failed_files, 0);
    eprintln!(
        "generation={} files={} bytes={} elapsed_ms={} peak_rss_kib={}",
        report.generation,
        report.indexed_files,
        report.discovered_bytes,
        started.elapsed().as_millis(),
        peak_rss_kib()
    );
    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    database.close().await;
}

#[cfg(target_os = "linux")]
fn peak_rss_kib() -> u64 {
    std::fs::read_to_string("/proc/self/status")
        .ok()
        .and_then(|status| {
            status.lines().find_map(|line| {
                line.strip_prefix("VmHWM:")?
                    .split_whitespace()
                    .next()?
                    .parse()
                    .ok()
            })
        })
        .unwrap_or_default()
}

#[cfg(not(target_os = "linux"))]
fn peak_rss_kib() -> u64 {
    0
}
