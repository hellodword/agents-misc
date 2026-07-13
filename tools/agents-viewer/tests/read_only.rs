use std::io::Read as _;

use agents_viewer::paths::{resolve_cache_paths, resolve_source_roots, validate_no_overlap};
use agents_viewer::permissions::{
    acquire_cache_lock, open_source_read_only, prepare_cache_directory, validate_cache_directory,
};
use tempfile::TempDir;

#[test]
fn source_root_symlink_is_allowed_but_file_symlink_is_rejected() {
    let temp = TempDir::new().unwrap();
    let real_home = temp.path().join("real-codex");
    let sessions = real_home.join("sessions/2026/01/01");
    std::fs::create_dir_all(&sessions).unwrap();
    let source = sessions.join("fixture.jsonl");
    std::fs::write(&source, b"{}\n").unwrap();
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&real_home, temp.path().join("codex-link")).unwrap();
        let roots = resolve_source_roots(&temp.path().join("codex-link")).unwrap();
        assert_eq!(roots.home, dunce::canonicalize(&real_home).unwrap());
        let opened = open_source_read_only(roots.active.as_ref().unwrap(), &source).unwrap();
        let mut text = String::new();
        (&opened.file).read_to_string(&mut text).unwrap();
        assert_eq!(text, "{}\n");

        let linked_file = sessions.join("linked.jsonl");
        std::os::unix::fs::symlink(&source, &linked_file).unwrap();
        assert!(open_source_read_only(roots.active.as_ref().unwrap(), &linked_file).is_err());
    }
}

#[test]
fn missing_source_subdirectory_is_an_empty_root() {
    let temp = TempDir::new().unwrap();
    let roots = resolve_source_roots(temp.path()).unwrap();
    assert!(roots.active.is_none());
    assert!(roots.archived.is_none());
}

#[test]
fn source_and_nonexistent_cache_overlap_is_rejected_both_directions() {
    let temp = TempDir::new().unwrap();
    let source = temp.path().join("source");
    std::fs::create_dir(&source).unwrap();
    assert!(validate_no_overlap(&source, &source.join("future/cache")).is_err());
    assert!(validate_no_overlap(&source.join("future/source"), temp.path()).is_err());
    assert!(resolve_cache_paths(&source, &source.join("cache")).is_err());
}

#[test]
fn lock_is_exclusive_and_stale_file_is_not_a_lock() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    prepare_cache_directory(&cache).unwrap();
    let lock_path = cache.join("viewer.lock");
    std::fs::write(&lock_path, b"stale").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        std::fs::set_permissions(&lock_path, std::fs::Permissions::from_mode(0o600)).unwrap();
    }
    let first = acquire_cache_lock(&lock_path).unwrap();
    assert!(acquire_cache_lock(&lock_path).is_err());
    drop(first);
    assert!(acquire_cache_lock(&lock_path).is_ok());
}

#[cfg(unix)]
#[test]
fn cache_permissions_fail_closed_and_read_only_source_still_opens() {
    use std::os::unix::fs::PermissionsExt as _;

    let temp = TempDir::new().unwrap();
    let insecure = temp.path().join("insecure");
    std::fs::create_dir(&insecure).unwrap();
    std::fs::set_permissions(&insecure, std::fs::Permissions::from_mode(0o755)).unwrap();
    assert!(validate_cache_directory(&insecure).is_err());

    let source_root = temp.path().join("source");
    std::fs::create_dir(&source_root).unwrap();
    let source = source_root.join("fixture.jsonl");
    std::fs::write(&source, b"{}\n").unwrap();
    std::fs::set_permissions(&source, std::fs::Permissions::from_mode(0o444)).unwrap();
    std::fs::set_permissions(&source_root, std::fs::Permissions::from_mode(0o555)).unwrap();
    let opened =
        open_source_read_only(&dunce::canonicalize(&source_root).unwrap(), &source).unwrap();
    assert_eq!(opened.identity.size, 3);
}
