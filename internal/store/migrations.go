package store

const migrations = []string{
	// v1：基础 schema
	`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	);
	CREATE TABLE models (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE model_versions (
		id TEXT PRIMARY KEY,
		model_id TEXT NOT NULL REFERENCES models(id),
		seq INTEGER NOT NULL,
		parent_id TEXT,
		payload_json TEXT NOT NULL,
		source TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(model_id, seq)
	);
	CREATE TABLE branches (
		id TEXT PRIMARY KEY,
		model_id TEXT NOT NULL REFERENCES models(id),
		name TEXT NOT NULL,
		base_version_id TEXT NOT NULL REFERENCES model_versions(id),
		head_event_id TEXT,
		status TEXT NOT NULL DEFAULT 'open',
		created_at TEXT NOT NULL
	);
	CREATE TABLE events (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL REFERENCES branches(id),
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		prev_event_id TEXT,
		decision TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		UNIQUE(branch_id, seq)
	);
	CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL REFERENCES branches(id),
		event_id TEXT NOT NULL REFERENCES events(id),
		model_version_id TEXT NOT NULL REFERENCES model_versions(id),
		strategy TEXT NOT NULL,
		budget INTEGER NOT NULL,
		status TEXT NOT NULL,
		frontier_json TEXT NOT NULL,
		stats_json TEXT NOT NULL,
		result_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE states (
		run_id TEXT NOT NULL REFERENCES runs(id),
		seq INTEGER NOT NULL,
		state_id TEXT NOT NULL,
		marking_json TEXT NOT NULL,
		canonical TEXT NOT NULL,
		depth INTEGER NOT NULL,
		PRIMARY KEY(run_id, state_id)
	);
	CREATE TABLE edges (
		run_id TEXT NOT NULL REFERENCES runs(id),
		seq INTEGER NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		transition_id TEXT NOT NULL,
		PRIMARY KEY(run_id, seq)
	);
	CREATE TABLE counterexamples (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL REFERENCES branches(id),
		kind TEXT NOT NULL,
		model_version_id TEXT NOT NULL REFERENCES model_versions(id),
		run_id TEXT NOT NULL REFERENCES runs(id),
		verdict TEXT NOT NULL DEFAULT '',
		rationale TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
	CREATE TABLE quantities (
		id TEXT PRIMARY KEY,
		owner_type TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		field TEXT NOT NULL,
		raw_value TEXT NOT NULL,
		raw_unit TEXT NOT NULL DEFAULT '',
		basis TEXT NOT NULL DEFAULT '',
		normalized_value INTEGER,
		normalized_unit TEXT NOT NULL DEFAULT '',
		diagnostic TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE dependencies (
		id TEXT PRIMARY KEY,
		src_type TEXT NOT NULL,
		src_id TEXT NOT NULL,
		dst_type TEXT NOT NULL,
		dst_id TEXT NOT NULL,
		kind TEXT NOT NULL
	);
	CREATE TABLE invalidations (
		id TEXT PRIMARY KEY,
		event_id TEXT NOT NULL REFERENCES events(id),
		affected_type TEXT NOT NULL,
		affected_id TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE conflicts (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL REFERENCES branches(id),
		base_event_id TEXT NOT NULL,
		left_event_id TEXT NOT NULL,
		right_event_id TEXT NOT NULL,
		diff_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`,
	// v2：事件备注列（演示状态迁移保留旧数据）
	`ALTER TABLE events ADD COLUMN note TEXT NOT NULL DEFAULT '';
	CREATE INDEX idx_events_branch ON events(branch_id, seq);
	CREATE INDEX idx_deps_target ON dependencies(dst_type, dst_id);`,
}
