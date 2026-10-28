package state

// Each role starts with a new database when this contract changes.
const SchemaVersion = 21

const controllerSchema = `
PRAGMA auto_vacuum=INCREMENTAL;
CREATE TABLE executions (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL, state TEXT NOT NULL, cleanup TEXT NOT NULL,
 created TEXT NOT NULL, ended TEXT, revision INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX execution_lifecycle ON executions(cleanup,state,sequence);
CREATE INDEX execution_retention ON executions(ended,cleanup);
CREATE TABLE labs (id TEXT PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE, body BLOB NOT NULL);
CREATE TABLE nix_jobs (id TEXT PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE, body BLOB NOT NULL);
CREATE TABLE nodes (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX nodes_execution ON nodes(task_id,position);
CREATE TABLE commands (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, node_id TEXT NOT NULL, needs_poll INTEGER NOT NULL,revision INTEGER NOT NULL,spec_file TEXT NOT NULL DEFAULT '',spec_size INTEGER NOT NULL DEFAULT 0,spec_sha256 TEXT NOT NULL DEFAULT '',body BLOB NOT NULL);
CREATE INDEX commands_execution ON commands(task_id,sequence);
CREATE INDEX commands_active ON commands(task_id,needs_poll,sequence);
CREATE TABLE endpoints (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE,position INTEGER NOT NULL,state TEXT NOT NULL,body BLOB NOT NULL);
CREATE INDEX endpoints_execution ON endpoints(task_id,sequence);
CREATE INDEX endpoints_active ON endpoints(task_id,state,sequence);
CREATE TABLE captures (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE, position INTEGER NOT NULL,state TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX captures_execution ON captures(task_id,sequence);
CREATE INDEX captures_active ON captures(task_id,state,sequence);
CREATE TABLE evidence (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL, position INTEGER NOT NULL, pinned INTEGER NOT NULL DEFAULT 0, protected INTEGER NOT NULL DEFAULT 1, expires TEXT, success INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX evidence_execution ON evidence(task_id,position);
CREATE INDEX evidence_pins ON evidence(pinned,task_id);
CREATE INDEX evidence_retention ON evidence(protected,pinned,success,expires);
CREATE TABLE operations (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, request_id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL, state TEXT NOT NULL, created TEXT NOT NULL, ended TEXT,revision INTEGER NOT NULL,intent_stage TEXT NOT NULL,intent_file TEXT NOT NULL,intent_size INTEGER NOT NULL,intent_sha256 TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX operations_execution ON operations(task_id,state);
CREATE INDEX operations_retention ON operations(state,ended);
CREATE INDEX operations_recovery ON operations(state,sequence);
CREATE TABLE transfers (sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL, state TEXT NOT NULL, updated TEXT NOT NULL, revision INTEGER NOT NULL, body BLOB NOT NULL);
CREATE INDEX transfers_lifecycle ON transfers(task_id,state,updated);
CREATE TABLE transfer_objects (transfer_id TEXT NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
 name TEXT NOT NULL, expected_size INTEGER NOT NULL, expected_sha256 TEXT NOT NULL, body BLOB, PRIMARY KEY(transfer_id,name));
CREATE TABLE command_inputs (task_id TEXT NOT NULL, command_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 file TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, eof INTEGER NOT NULL,
 delivered INTEGER NOT NULL DEFAULT 0, written INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(task_id,command_id,sequence),
 FOREIGN KEY(command_id) REFERENCES commands(id) ON DELETE CASCADE);
CREATE TABLE events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX events_execution ON events(task_id,sequence);
`

const workerSchema = `
CREATE TABLE worker_jobs (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL, cleanup TEXT NOT NULL, request BLOB NOT NULL, execution BLOB NOT NULL,
 started TEXT NOT NULL, deadline TEXT NOT NULL,handoff_complete INTEGER NOT NULL,copies_released INTEGER NOT NULL,
 manifest_digest TEXT NOT NULL,handoff_finished TEXT,file_count INTEGER NOT NULL);
CREATE INDEX worker_recovery ON worker_jobs(cleanup,copies_released,sequence);
CREATE INDEX worker_retention ON worker_jobs(handoff_finished,copies_released);
CREATE TABLE worker_files (sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL REFERENCES worker_jobs(id) ON DELETE CASCADE,name TEXT NOT NULL,root TEXT NOT NULL,
 relative TEXT NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL);
CREATE INDEX worker_files_execution ON worker_files(task_id,sequence);
`

const metadataSchema = `
CREATE TABLE store (singleton INTEGER PRIMARY KEY CHECK(singleton=1), role TEXT NOT NULL, instance_id TEXT NOT NULL);
PRAGMA user_version=21;
`
