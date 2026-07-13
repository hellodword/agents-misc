CREATE TABLE app_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE source_files (
    id INTEGER PRIMARY KEY,
    root_kind TEXT NOT NULL CHECK (root_kind IN ('active', 'archived')),
    relative_path TEXT NOT NULL,
    file_key TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    head_hash TEXT,
    tail_hash TEXT,
    checkpoint_offset INTEGER NOT NULL DEFAULT 0,
    checkpoint_line INTEGER NOT NULL DEFAULT 0,
    checkpoint_hash TEXT,
    session_id TEXT,
    scan_state TEXT NOT NULL DEFAULT 'pending',
    scan_token TEXT,
    last_error TEXT,
    seen_generation INTEGER NOT NULL DEFAULT 0,
    UNIQUE (root_kind, relative_path)
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source_file_id INTEGER NOT NULL UNIQUE REFERENCES source_files(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    parent_thread_id TEXT,
    cwd TEXT,
    title TEXT NOT NULL,
    preview TEXT NOT NULL,
    created_at_micros INTEGER NOT NULL,
    updated_at_micros INTEGER NOT NULL,
    archived INTEGER NOT NULL CHECK (archived IN (0, 1)),
    cli_version TEXT,
    provider TEXT,
    history_line INTEGER,
    git_branch TEXT,
    git_commit TEXT,
    entry_count INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL,
    completeness TEXT NOT NULL,
    diagnostic_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX sessions_updated_at_idx ON sessions(updated_at_micros DESC, id);
CREATE INDEX sessions_source_kind_idx ON sessions(source_kind);
CREATE INDEX sessions_archived_idx ON sessions(archived);
CREATE INDEX sessions_parent_thread_id_idx ON sessions(parent_thread_id);

CREATE TABLE entries (
    rowid INTEGER PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    timestamp_micros INTEGER,
    kind TEXT NOT NULL,
    presentation TEXT NOT NULL CHECK (presentation IN ('user', 'response', 'technical', 'internal')),
    role TEXT,
    phase TEXT,
    tool_kind TEXT,
    tool_status TEXT,
    title TEXT NOT NULL,
    primary_text TEXT NOT NULL,
    secondary_text TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    id_basis TEXT NOT NULL,
    call_id TEXT,
    parent_entry_id TEXT,
    default_collapsed INTEGER NOT NULL CHECK (default_collapsed IN (0, 1)),
    searchable INTEGER NOT NULL CHECK (searchable IN (0, 1)),
    primary_bytes INTEGER NOT NULL,
    secondary_bytes INTEGER NOT NULL,
    UNIQUE (session_id, sequence)
);

CREATE INDEX entries_session_sequence_idx ON entries(session_id, sequence);
CREATE INDEX entries_call_id_idx ON entries(call_id);

CREATE TABLE raw_records (
    id TEXT PRIMARY KEY,
    source_file_id INTEGER NOT NULL REFERENCES source_files(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    line_no INTEGER NOT NULL,
    byte_offset INTEGER NOT NULL,
    byte_length INTEGER NOT NULL,
    envelope_type TEXT NOT NULL,
    parse_status TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    utf8 INTEGER NOT NULL CHECK (utf8 IN (0, 1)),
    oversize INTEGER NOT NULL CHECK (oversize IN (0, 1)),
    hex_preview TEXT,
    UNIQUE (source_file_id, line_no)
);

CREATE INDEX raw_records_session_line_idx ON raw_records(session_id, line_no);

CREATE TABLE entry_raw_refs (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    raw_id TEXT NOT NULL REFERENCES raw_records(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    PRIMARY KEY (entry_id, raw_id)
);

CREATE TABLE diagnostics (
    id INTEGER PRIMARY KEY,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    source_file_id INTEGER REFERENCES source_files(id) ON DELETE CASCADE,
    severity TEXT NOT NULL,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    first_seen_at_micros INTEGER NOT NULL,
    last_seen_at_micros INTEGER NOT NULL,
    count INTEGER NOT NULL DEFAULT 1,
    UNIQUE (source_file_id, dedup_key)
);

CREATE INDEX diagnostics_session_severity_idx ON diagnostics(session_id, severity);

CREATE TABLE staged_sessions (
    scan_token TEXT NOT NULL,
    id TEXT NOT NULL,
    source_file_id INTEGER NOT NULL,
    source_kind TEXT NOT NULL,
    parent_thread_id TEXT,
    cwd TEXT,
    title TEXT NOT NULL,
    preview TEXT NOT NULL,
    created_at_micros INTEGER NOT NULL,
    updated_at_micros INTEGER NOT NULL,
    archived INTEGER NOT NULL,
    cli_version TEXT,
    provider TEXT,
    history_line INTEGER,
    git_branch TEXT,
    git_commit TEXT,
    entry_count INTEGER NOT NULL,
    index_state TEXT NOT NULL,
    completeness TEXT NOT NULL,
    diagnostic_count INTEGER NOT NULL,
    PRIMARY KEY (scan_token, id)
);

CREATE TABLE staged_entries (
    scan_token TEXT NOT NULL,
    id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    timestamp_micros INTEGER,
    kind TEXT NOT NULL,
    presentation TEXT NOT NULL CHECK (presentation IN ('user', 'response', 'technical', 'internal')),
    role TEXT,
    phase TEXT,
    tool_kind TEXT,
    tool_status TEXT,
    title TEXT NOT NULL,
    primary_text TEXT NOT NULL,
    secondary_text TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    id_basis TEXT NOT NULL,
    call_id TEXT,
    parent_entry_id TEXT,
    default_collapsed INTEGER NOT NULL,
    searchable INTEGER NOT NULL,
    primary_bytes INTEGER NOT NULL,
    secondary_bytes INTEGER NOT NULL,
    PRIMARY KEY (scan_token, id)
);

CREATE TABLE staged_raw_records (
    scan_token TEXT NOT NULL,
    id TEXT NOT NULL,
    source_file_id INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    line_no INTEGER NOT NULL,
    byte_offset INTEGER NOT NULL,
    byte_length INTEGER NOT NULL,
    envelope_type TEXT NOT NULL,
    parse_status TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    utf8 INTEGER NOT NULL,
    oversize INTEGER NOT NULL,
    hex_preview TEXT,
    PRIMARY KEY (scan_token, id)
);

CREATE TABLE staged_entry_raw_refs (
    scan_token TEXT NOT NULL,
    entry_id TEXT NOT NULL,
    raw_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    PRIMARY KEY (scan_token, entry_id, raw_id)
);

CREATE TABLE staged_diagnostics (
    scan_token TEXT NOT NULL,
    session_id TEXT,
    source_file_id INTEGER,
    severity TEXT NOT NULL,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    first_seen_at_micros INTEGER NOT NULL,
    last_seen_at_micros INTEGER NOT NULL,
    count INTEGER NOT NULL,
    PRIMARY KEY (scan_token, dedup_key)
);

CREATE VIRTUAL TABLE entries_fts USING fts5(
    title,
    primary_text,
    secondary_text,
    content = 'entries',
    content_rowid = 'rowid',
    tokenize = 'trigram'
);

CREATE TRIGGER entries_fts_insert AFTER INSERT ON entries
WHEN new.searchable = 1
BEGIN
    INSERT INTO entries_fts(rowid, title, primary_text, secondary_text)
    VALUES (new.rowid, new.title, new.primary_text, new.secondary_text);
END;

CREATE TRIGGER entries_fts_delete AFTER DELETE ON entries
WHEN old.searchable = 1
BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, primary_text, secondary_text)
    VALUES ('delete', old.rowid, old.title, old.primary_text, old.secondary_text);
END;

CREATE TRIGGER entries_fts_update AFTER UPDATE ON entries
BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, primary_text, secondary_text)
    SELECT 'delete', old.rowid, old.title, old.primary_text, old.secondary_text
    WHERE old.searchable = 1;
    INSERT INTO entries_fts(rowid, title, primary_text, secondary_text)
    SELECT new.rowid, new.title, new.primary_text, new.secondary_text
    WHERE new.searchable = 1;
END;
