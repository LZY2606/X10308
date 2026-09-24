package store

// migrations is the ordered, append-only schema history. Migration tests
// build a database at an older version and verify state carries forward.
var migrations = []string{
	// v1: core versioned workspace
	`CREATE TABLE models(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE model_versions(
		id TEXT PRIMARY KEY,
		model_id TEXT NOT NULL,
		version_no INTEGER NOT NULL,
		parent_id TEXT,
		change_kind TEXT NOT NULL,
		payload TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE strategies(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		budget INTEGER NOT NULL,
		version_no INTEGER NOT NULL,
		revision TEXT,
		created_at TEXT NOT NULL
	);
	CREATE TABLE runs(
		id TEXT PRIMARY KEY,
		model_version_id TEXT NOT NULL,
		strategy_id TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE branches(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		base_seq INTEGER NOT NULL,
		head_seq INTEGER NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE events(
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL,
		branch_id TEXT,
		kind TEXT NOT NULL,
		payload TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE rulings(
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL,
		target_kind TEXT NOT NULL,
		target_id TEXT NOT NULL,
		decision TEXT NOT NULL,
		rationale TEXT,
		event_seq INTEGER NOT NULL,
		undone INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE TABLE counterexamples(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		description TEXT,
		baseline_refs TEXT NOT NULL,
		mutation TEXT NOT NULL,
		result_refs TEXT NOT NULL,
		adopted INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);`,
	// v2: derived-layer concerns — staleness from scoped invalidation, and
	// import diagnostics kept next to the values they describe.
	`ALTER TABLE runs ADD COLUMN stale INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE diagnostics(
		id TEXT PRIMARY KEY,
		owner_kind TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		message TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX idx_events_branch ON events(branch_id, seq);
	CREATE INDEX idx_rulings_branch ON rulings(branch_id);`,
}
