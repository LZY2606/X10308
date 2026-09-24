package store

// schema is migration #1. All tables are append-only: rows are never updated
// except branch pointers, decision revisions and invalidation markers, which
// are versioned in event_log.
const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied TEXT NOT NULL
);

INSERT INTO schema_migrations(version, applied)
SELECT 1, '2026-09-24'
WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = 1);

-- Global append-only ledger. Every judgement (enablement clauses kept on
-- runs, adjudication, invalidation, conflict, ...) is an event.
CREATE TABLE IF NOT EXISTS event_log (
  seq         INTEGER PRIMARY KEY,
  at_event    TEXT NOT NULL,
  event_type  TEXT NOT NULL,
  actor       TEXT NOT NULL,
  branch_id   TEXT,
  ref_type    TEXT,
  ref_id      TEXT,
  payload_json TEXT NOT NULL DEFAULT '{}',
  base_rev    INTEGER
);

-- One transactional import batch. Failed imports leave no row and roll back.
CREATE TABLE IF NOT EXISTS import_batch (
  id          TEXT PRIMARY KEY,
  at_event    TEXT NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('committed','rolled_back')),
  pack_version TEXT,
  note        TEXT
);

-- Versioned source material. Raw bytes/representation are immutable once
-- committed; re-interpretation creates a new material version and branch.
CREATE TABLE IF NOT EXISTS material (
  id          TEXT NOT NULL,
  version     INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  title       TEXT NOT NULL,
  raw_json    TEXT NOT NULL,
  sha256      TEXT NOT NULL,
  import_batch TEXT NOT NULL REFERENCES import_batch(id),
  supersedes  TEXT,
  PRIMARY KEY (id, version)
);

CREATE TABLE IF NOT EXISTS material_diagnostic (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  material_id TEXT NOT NULL,
  version     INTEGER NOT NULL,
  path        TEXT NOT NULL,
  severity    TEXT NOT NULL,
  code        TEXT NOT NULL,
  message     TEXT NOT NULL,
  raw_value   TEXT
);

-- A branch pins an ordered set of material versions plus a parent branch.
CREATE TABLE IF NOT EXISTS branch (
  id          TEXT PRIMARY KEY,
  seq         INTEGER NOT NULL UNIQUE,
  title       TEXT NOT NULL,
  parent_id   TEXT REFERENCES branch(id),
  created_event INTEGER NOT NULL,
  open        INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS branch_material (
  branch_id   TEXT NOT NULL REFERENCES branch(id),
  material_id TEXT NOT NULL,
  version     INTEGER NOT NULL,
  slot        INTEGER NOT NULL,
  PRIMARY KEY (branch_id, material_id)
);

-- Derived interpretation of a net material version.
CREATE TABLE IF NOT EXISTS model (
  id              TEXT PRIMARY KEY,
  seq             INTEGER NOT NULL UNIQUE,
  branch_id       TEXT NOT NULL REFERENCES branch(id),
  material_id     TEXT NOT NULL,
  material_version INTEGER NOT NULL,
  rules_version   TEXT NOT NULL,
  derived_json    TEXT NOT NULL,
  created_event   INTEGER NOT NULL
);

-- A run of state-space exploration / coverability with fixed parameters.
CREATE TABLE IF NOT EXISTS run (
  id              TEXT PRIMARY KEY,
  seq             INTEGER NOT NULL UNIQUE,
  branch_id       TEXT NOT NULL REFERENCES branch(id),
  model_id        TEXT NOT NULL REFERENCES model(id),
  rules_version   TEXT NOT NULL,
  strategy        TEXT NOT NULL,
  fire_budget     INTEGER NOT NULL,
  state_budget    INTEGER NOT NULL,
  target_json     TEXT,
  result_json     TEXT NOT NULL,
  cover_json      TEXT,
  created_event   INTEGER NOT NULL
);

-- Counterexample / finding archive: both adopted and rejected outcomes.
CREATE TABLE IF NOT EXISTS counterexample (
  id          TEXT PRIMARY KEY,
  seq         INTEGER NOT NULL UNIQUE,
  branch_id   TEXT NOT NULL REFERENCES branch(id),
  run_id      TEXT NOT NULL REFERENCES run(id),
  scenario    TEXT NOT NULL,
  title       TEXT NOT NULL,
  detail_json TEXT NOT NULL,
  verdict     TEXT NOT NULL DEFAULT 'pending'
              CHECK (verdict IN ('pending','accepted','rejected')),
  rev         INTEGER NOT NULL DEFAULT 1,
  decided_event INTEGER,
  decided_by  TEXT
);

-- Audit rows for every adjudication attempt, including rejected conflicts.
CREATE TABLE IF NOT EXISTS decision (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  counterexample_id TEXT NOT NULL REFERENCES counterexample(id),
  at_event      TEXT NOT NULL,
  actor         TEXT NOT NULL,
  action        TEXT NOT NULL,
  base_rev      INTEGER NOT NULL,
  new_rev       INTEGER,
  status        TEXT NOT NULL CHECK (status IN ('applied','conflict','undone')),
  note          TEXT,
  payload_json  TEXT NOT NULL DEFAULT '{}'
);

-- Provenance / invalidation graph between derived objects.
CREATE TABLE IF NOT EXISTS dependency (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  consumer_type TEXT NOT NULL,
  consumer_id   TEXT NOT NULL,
  supplier_type TEXT NOT NULL,
  supplier_id   TEXT NOT NULL,
  supplier_version INTEGER NOT NULL,
  rel           TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS invalidation (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  seq         INTEGER NOT NULL UNIQUE,
  at_event    TEXT NOT NULL,
  scope_json  TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  cause_type  TEXT NOT NULL,
  cause_id    TEXT NOT NULL,
  cause_version INTEGER NOT NULL
);
`
