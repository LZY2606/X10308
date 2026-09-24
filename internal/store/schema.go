package store

// schemaV1 is the originally shipped database. Migration tests build a
// database at this version and verify migration to the current schema without
// losing identities or source bytes.
const schemaV1 = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE blobs (
  id TEXT PRIMARY KEY,
  sha256 TEXT NOT NULL,
  bytes BLOB NOT NULL,
  imported_at TEXT NOT NULL,
  note TEXT DEFAULT ''
);
CREATE TABLE branches (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  parent_branch TEXT DEFAULT '',
  forked_from_event TEXT DEFAULT '',
  base_event TEXT DEFAULT '',
  created_at TEXT NOT NULL,
  kind TEXT DEFAULT 'initial',
  note TEXT DEFAULT ''
);
CREATE TABLE source_docs (
  id TEXT PRIMARY KEY,
  branch_id TEXT NOT NULL,
  bundle_id TEXT DEFAULT '',
  model_version TEXT DEFAULT '',
  title TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE source_sections (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL,
  section TEXT NOT NULL,
  version TEXT NOT NULL,
  raw_blob TEXT NOT NULL,
  content_json TEXT NOT NULL,
  UNIQUE(doc_id, section)
);
CREATE TABLE events (
  id TEXT PRIMARY KEY,
  branch_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  created_at TEXT NOT NULL,
  actor TEXT DEFAULT 'system',
  payload_json TEXT NOT NULL,
  net_id TEXT DEFAULT '',
  run_id TEXT DEFAULT '',
  candidate_id TEXT DEFAULT '',
  UNIQUE(branch_id, seq)
);
CREATE TABLE explorations (
  run_id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL,
  branch_id TEXT NOT NULL,
  net_id TEXT NOT NULL,
  result_json TEXT NOT NULL,
  status TEXT NOT NULL
);
CREATE TABLE frontiers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  state_id TEXT NOT NULL,
  marking TEXT NOT NULL,
  depth INTEGER NOT NULL,
  from_state TEXT DEFAULT '',
  transition TEXT DEFAULT '',
  reason TEXT NOT NULL
);
CREATE TABLE decisions (
  id TEXT PRIMARY KEY,
  branch_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  candidate_id TEXT NOT NULL,
  verdict TEXT NOT NULL,
  note TEXT DEFAULT '',
  undone_by TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
`

// schemaV2 adds explicit invalidation scope (supersession existed implicitly).
const schemaV2Statements = `
ALTER TABLE events ADD COLUMN superseded_by TEXT DEFAULT '';
CREATE TABLE IF NOT EXISTS invalidations (
  id TEXT PRIMARY KEY,
  branch_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  changed_section TEXT NOT NULL,
  changed_source TEXT NOT NULL,
  affected_event TEXT NOT NULL,
  affected_kind TEXT NOT NULL,
  reason TEXT NOT NULL
);
`

const currentSchemaVersion = "2"
