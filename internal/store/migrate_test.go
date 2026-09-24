package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration test: data written at schema v1 must survive every upgrade
// step, and each migration must add exactly its promised structure.
func TestMigrateStepwise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Apply only v1 and write old-shape data.
	if err := migrate(db, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sources(id,kind,name,raw,hash,unit,basis,created_at)
		VALUES('src-old','model','old','{}','h','token','px','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("write at v1: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events(id,branch_id,kind,payload_json,model_version_id,rule_version,created_at)
		VALUES('ev-old','br-1','import','{}','',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("event at v1: %v", err)
	}

	// v2 must add candidates/dependencies/diagnostics without touching v1 data.
	if err := migrate(db, 2); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("v1 data lost at v2: n=%d err=%v", n, err)
	}
	if _, err := db.Exec(`INSERT INTO dependencies(derived_kind,derived_id,source_id,version_seq) VALUES('run','r1','src-old',1)`); err != nil {
		t.Fatalf("v2 must provide dependencies: %v", err)
	}

	// v3 must add counterexamples and candidates.note.
	if err := migrate(db, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO candidates(id,branch_id,run_id,kind,payload_json,status,note,created_at)
		VALUES('c1','br-1','r1','deadlock','{}','pending','n','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("v3 must provide candidates.note: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO counterexamples(id,name,scenario,description,branch_id,baseline_run_id,variant_run_id,changed_source_id,judgment,created_at)
		VALUES('x1','n','s','d','br-1','r1','r1','src-old','pending','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("v3 must provide counterexamples: %v", err)
	}
	// Old event still readable with its original identity.
	var id string
	if err := db.QueryRow(`SELECT id FROM events WHERE seq=1`).Scan(&id); err != nil || id != "ev-old" {
		t.Fatalf("event identity lost across migrations: %q %v", id, err)
	}
	// Re-running migration is a no-op.
	if err := migrate(db, schemaVersion); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil || v != 3 {
		t.Fatalf("schema version = %d", v)
	}
}
