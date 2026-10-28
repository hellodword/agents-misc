package state

// SchemaVersion deliberately has no upgrade path from the document store.
const SchemaVersion = 20

const schema = `
PRAGMA auto_vacuum=INCREMENTAL;
CREATE TABLE executions (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL, state TEXT NOT NULL, cleanup TEXT NOT NULL,
 created TEXT NOT NULL, ended TEXT, body BLOB NOT NULL);
CREATE INDEX execution_lifecycle ON executions(cleanup,state,sequence);
CREATE INDEX execution_retention ON executions(ended,cleanup);
CREATE TABLE labs (id TEXT PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE, body BLOB NOT NULL);
CREATE TABLE nix_jobs (id TEXT PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE, body BLOB NOT NULL);
CREATE TABLE nodes (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX nodes_execution ON nodes(task_id,position);
CREATE TABLE commands (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX commands_execution ON commands(task_id,position);
CREATE TABLE endpoints (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX endpoints_execution ON endpoints(task_id,position);
CREATE TABLE captures (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX captures_execution ON captures(task_id,position);
CREATE TABLE evidence (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, pinned INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX evidence_execution ON evidence(task_id,position);
CREATE INDEX evidence_pins ON evidence(pinned,task_id);
CREATE TABLE operations (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, request_id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL, state TEXT NOT NULL, created TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX operations_execution ON operations(task_id,state);
CREATE INDEX operations_recovery ON operations(state,sequence);
CREATE TABLE dispatch_intents (operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE CASCADE,
 file TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL);
CREATE TABLE transfers (sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL, state TEXT NOT NULL, updated TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX transfers_lifecycle ON transfers(task_id,state,updated);
CREATE TABLE transfer_objects (transfer_id TEXT NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
 name TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(transfer_id,name));
CREATE TABLE command_specs (task_id TEXT NOT NULL, command_id TEXT NOT NULL,
 file TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, PRIMARY KEY(task_id,command_id));
CREATE TABLE command_inputs (task_id TEXT NOT NULL, command_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 file TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, eof INTEGER NOT NULL,
 delivered INTEGER NOT NULL DEFAULT 0, consumed INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(task_id,command_id,sequence),
 FOREIGN KEY(task_id,command_id) REFERENCES command_specs(task_id,command_id) ON DELETE CASCADE);
CREATE TABLE events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX events_execution ON events(task_id,sequence);
CREATE TABLE worker_jobs (sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL, cleanup TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE worker_files (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES worker_jobs(id) ON DELETE CASCADE,
 position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX worker_files_execution ON worker_files(task_id,position);
CREATE TABLE revisions (key TEXT PRIMARY KEY, owner_id TEXT NOT NULL, revision INTEGER NOT NULL, digest TEXT NOT NULL);
CREATE INDEX revisions_owner ON revisions(owner_id);
PRAGMA user_version=20;
`
