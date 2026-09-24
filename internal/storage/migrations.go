package storage

// migrations are applied in order. Each is wrapped in its own transaction and
// recorded in schema_migrations. Migrations are append-only: old databases are
// upgraded through every step, which is what the state migration tests pin.
//
// docs are the single source of truth: every normalized view is re-derived
// from the stored raw bytes. That makes replay to any old event exact.
var migrations = []string{
	`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE TABLE workspace_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE branches (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		parent_branch TEXT,
		parent_event_seq INTEGER,
		head_event_seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE TABLE events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		branch_id TEXT NOT NULL REFERENCES branches(id),
		kind TEXT NOT NULL,
		actor TEXT NOT NULL DEFAULT 'reviewer',
		note TEXT NOT NULL DEFAULT '',
		payload TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX events_branch ON events(branch_id, seq);
	CREATE TABLE docs (
		branch_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		version INTEGER NOT NULL,
		doc_id TEXT NOT NULL,
		raw TEXT NOT NULL,
		media TEXT NOT NULL DEFAULT 'application/json',
		created_event_seq INTEGER NOT NULL,
		resolved INTEGER NOT NULL DEFAULT 1,
		diagnostics TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY (branch_id, kind, version)
	);`,
	`CREATE TABLE explorations (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		created_event_seq INTEGER NOT NULL,
		status TEXT NOT NULL,
		strategy TEXT NOT NULL,
		max_states INTEGER NOT NULL,
		max_steps INTEGER NOT NULL,
		states_seen INTEGER NOT NULL,
		steps_used INTEGER NOT NULL,
		target_reachable INTEGER,
		target_covered INTEGER,
		initial_state_id TEXT NOT NULL,
		enablement_rule TEXT NOT NULL,
		canonical_rule TEXT NOT NULL,
		budget_rule TEXT NOT NULL,
		diagnostics TEXT NOT NULL DEFAULT '[]',
		invalidated_event_seq INTEGER,
		invalidated_reason TEXT NOT NULL DEFAULT '',
		invalidation_scope TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY (branch_id, exploration_id)
	);
	CREATE TABLE states (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		state_id TEXT NOT NULL,
		idx INTEGER NOT NULL,
		depth INTEGER NOT NULL,
		omega INTEGER NOT NULL DEFAULT 0,
		tokens TEXT NOT NULL,
		is_initial INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (branch_id, exploration_id, state_id)
	);
	CREATE TABLE state_edges (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		transition_id TEXT NOT NULL,
		idx INTEGER NOT NULL,
		PRIMARY KEY (branch_id, exploration_id, idx)
	);
	CREATE TABLE deadlocks (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		state_id TEXT NOT NULL,
		reasons_json TEXT NOT NULL,
		idx INTEGER NOT NULL,
		PRIMARY KEY (branch_id, exploration_id, idx)
	);
	CREATE TABLE frontier (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		state_id TEXT NOT NULL,
		transition_id TEXT NOT NULL,
		reason TEXT NOT NULL,
		idx INTEGER NOT NULL,
		PRIMARY KEY (branch_id, exploration_id, idx)
	);
	CREATE TABLE candidates (
		branch_id TEXT NOT NULL,
		exploration_id TEXT NOT NULL,
		idx INTEGER NOT NULL,
		kind TEXT NOT NULL,
		options_json TEXT NOT NULL,
		chosen TEXT,
		decided_event_seq INTEGER,
		decided_by TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (branch_id, exploration_id, idx)
	);
	CREATE TABLE conflicts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		branch_id TEXT NOT NULL,
		base_event_seq INTEGER NOT NULL,
		attempted_by TEXT NOT NULL DEFAULT '',
		diff_json TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		resolved INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE import_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		branch_id TEXT,
		status TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		doc_kinds_json TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
}
