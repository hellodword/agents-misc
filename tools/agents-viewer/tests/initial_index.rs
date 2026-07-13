use agents_viewer::index::InitialIndexPolicy;
use agents_viewer::index::scanner::discover_sources;
use tempfile::TempDir;

#[test]
fn discovery_reports_files_excluded_by_session_creation_time() {
    let temp = TempDir::new().unwrap();
    let source = temp.path().join("source");
    let sessions = source.join("sessions/2025/01/02");
    std::fs::create_dir_all(&sessions).unwrap();
    let rollout =
        sessions.join("rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl");
    std::fs::copy(
        concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/fixtures/rollouts/v0_120.jsonl"
        ),
        &rollout,
    )
    .unwrap();
    let size = std::fs::metadata(&rollout).unwrap().len();
    let roots = agents_viewer::paths::resolve_source_roots(&source).unwrap();
    let cutoff = chrono::DateTime::parse_from_rfc3339("2025-01-03T00:00:00Z")
        .unwrap()
        .timestamp_micros();
    let excluded = discover_sources(
        &roots,
        1024 * 1024,
        1,
        cutoff,
        InitialIndexPolicy {
            days: 0,
            cutoff_micros: Some(cutoff),
        },
    );
    assert!(excluded.sources.is_empty());
    assert_eq!(excluded.excluded_files, 1);
    assert_eq!(excluded.excluded_bytes, size);

    let included = discover_sources(&roots, 1024 * 1024, 2, cutoff, InitialIndexPolicy::all());
    assert_eq!(included.sources.len(), 1);
    assert_eq!(included.excluded_files, 0);
}
