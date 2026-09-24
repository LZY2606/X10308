// Package store persists the workspace in SQLite. Sources (raw bytes) and
// interpretations (branches, runs, rulings) are kept strictly separate;
// the event log is append-only so any historical view can be restored.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database and migrates to latest.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// InTx runs fn in a transaction. Any error rolls the whole change back:
// a failed import never leaves half a record behind.
func (s *Store) InTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// NewID returns a stable random ID with the given prefix.
func NewID(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

// Hash returns the content hash of raw bytes.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Source is an imported raw artifact. Raw bytes are never rewritten.
type Source struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // model|weights|marking|capacities|strategy|budget|document
	Name      string `json:"name"`
	Raw       []byte `json:"raw"`
	Hash      string `json:"hash"`
	Unit      string `json:"unit"`
	Basis     string `json:"basis"`
	CreatedAt string `json:"created_at"`
}

// ModelVersion is one immutable, versioned snapshot of the normalized net
// plus every parameter that influences exploration.
type ModelVersion struct {
	ID        string   `json:"id"`
	Seq       int      `json:"seq"`
	ParentID  string   `json:"parent_id"`
	Label     string   `json:"label"`
	ModelJSON string   `json:"model_json"`
	SourceIDs []string `json:"source_ids"`
	CreatedAt string   `json:"created_at"`
}

// Branch is a line of interpretation. Re-interpretation forks a new
// branch; old branches stay readable forever.
type Branch struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ParentID       string `json:"parent_id"`
	ModelVersionID string `json:"model_version_id"`
	RuleVersion    int    `json:"rule_version"`
	HeadSeq        int64  `json:"head_seq"`
	CreatedAt      string `json:"created_at"`
}

// Run is one exploration result with its full state space as JSON.
type Run struct {
	ID             string `json:"id"`
	BranchID       string `json:"branch_id"`
	ModelVersionID string `json:"model_version_id"`
	RuleVersion    int    `json:"rule_version"`
	Strategy       string `json:"strategy"`
	Budget         int    `json:"budget"`
	Status         string `json:"status"`
	ResultJSON     string `json:"result_json"`
	CreatedAt      string `json:"created_at"`
}

// Candidate is one of possibly several legal options kept for ruling.
type Candidate struct {
	ID          string `json:"id"`
	BranchID    string `json:"branch_id"`
	RunID       string `json:"run_id"`
	Kind        string `json:"kind"` // deadlock|target|frontier|counterexample
	PayloadJSON string `json:"payload_json"`
	Status      string `json:"status"` // pending|accepted|rejected
	Note        string `json:"note"`
	CreatedAt   string `json:"created_at"`
}

// Event is one machine-readable judgment record in the append-only log.
type Event struct {
	Seq            int64  `json:"seq"`
	ID             string `json:"id"`
	BranchID       string `json:"branch_id"`
	Kind           string `json:"kind"` // import|version|reinterpret|run|ruling|undo|invalidate|adjust|diagnostic
	PayloadJSON    string `json:"payload_json"`
	ModelVersionID string `json:"model_version_id"`
	RuleVersion    int    `json:"rule_version"`
	CreatedAt      string `json:"created_at"`
}

// Dependency links a derived object to a source version it was computed
// from; used to compute invalidation scope when a source changes.
type Dependency struct {
	DerivedKind string `json:"derived_kind"`
	DerivedID   string `json:"derived_id"`
	SourceID    string `json:"source_id"`
	VersionSeq  int    `json:"version_seq"`
}

// DiagnosticRow keeps an unconvertible raw value with its message.
type DiagnosticRow struct {
	ID        string `json:"id"`
	SourceID  string `json:"source_id"`
	Message   string `json:"message"`
	RawValue  string `json:"raw_value"`
	CreatedAt string `json:"created_at"`
}

// Counterexample is one archived baseline-vs-variant pair.
type Counterexample struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Scenario        string `json:"scenario"`
	Description     string `json:"description"`
	BranchID        string `json:"branch_id"`
	BaselineRunID   string `json:"baseline_run_id"`
	VariantRunID    string `json:"variant_run_id"`
	ChangedSourceID string `json:"changed_source_id"`
	Judgment        string `json:"judgment"` // pending|accepted|rejected
	CreatedAt       string `json:"created_at"`
}

// ---- writes ----

// CreateSource inserts a source inside tx.
func CreateSource(tx *sql.Tx, src *Source) error {
	if src.ID == "" {
		src.ID = NewID("src")
	}
	if src.Hash == "" {
		src.Hash = Hash(src.Raw)
	}
	if src.CreatedAt == "" {
		src.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO sources(id,kind,name,raw,hash,unit,basis,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		src.ID, src.Kind, src.Name, src.Raw, src.Hash, src.Unit, src.Basis, src.CreatedAt)
	return err
}

// CreateModelVersion inserts a model version inside tx.
func CreateModelVersion(tx *sql.Tx, mv *ModelVersion) error {
	if mv.ID == "" {
		mv.ID = NewID("mv")
	}
	if mv.CreatedAt == "" {
		mv.CreatedAt = now()
	}
	ids, _ := json.Marshal(mv.SourceIDs)
	_, err := tx.Exec(`INSERT INTO model_versions(id,seq,parent_id,label,model_json,source_ids,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		mv.ID, mv.Seq, mv.ParentID, mv.Label, mv.ModelJSON, string(ids), mv.CreatedAt)
	return err
}

// CreateBranch inserts a branch inside tx.
func CreateBranch(tx *sql.Tx, b *Branch) error {
	if b.ID == "" {
		b.ID = NewID("br")
	}
	if b.CreatedAt == "" {
		b.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO branches(id,name,parent_id,model_version_id,rule_version,head_seq,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		b.ID, b.Name, b.ParentID, b.ModelVersionID, b.RuleVersion, b.HeadSeq, b.CreatedAt)
	return err
}

// CreateRun inserts a run inside tx.
func CreateRun(tx *sql.Tx, r *Run) error {
	if r.ID == "" {
		r.ID = NewID("run")
	}
	if r.CreatedAt == "" {
		r.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO runs(id,branch_id,model_version_id,rule_version,strategy,budget,status,result_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		r.ID, r.BranchID, r.ModelVersionID, r.RuleVersion, r.Strategy, r.Budget, r.Status, r.ResultJSON, r.CreatedAt)
	return err
}

// CreateCandidate inserts a candidate inside tx.
func CreateCandidate(tx *sql.Tx, c *Candidate) error {
	if c.ID == "" {
		c.ID = NewID("cand")
	}
	if c.Status == "" {
		c.Status = "pending"
	}
	if c.CreatedAt == "" {
		c.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO candidates(id,branch_id,run_id,kind,payload_json,status,note,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		c.ID, c.BranchID, c.RunID, c.Kind, c.PayloadJSON, c.Status, c.Note, c.CreatedAt)
	return err
}

// AppendEvent appends an event and returns its sequence number.
func AppendEvent(tx *sql.Tx, e *Event) (int64, error) {
	if e.ID == "" {
		e.ID = NewID("ev")
	}
	if e.CreatedAt == "" {
		e.CreatedAt = now()
	}
	res, err := tx.Exec(`INSERT INTO events(id,branch_id,kind,payload_json,model_version_id,rule_version,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		e.ID, e.BranchID, e.Kind, e.PayloadJSON, e.ModelVersionID, e.RuleVersion, e.CreatedAt)
	if err != nil {
		return 0, err
	}
	e.Seq, err = res.LastInsertId()
	return e.Seq, err
}

// SetBranchHead moves the branch head inside tx.
func SetBranchHead(tx *sql.Tx, branchID string, seq int64) error {
	_, err := tx.Exec(`UPDATE branches SET head_seq=? WHERE id=?`, seq, branchID)
	return err
}

// SetCandidateStatus updates a candidate's ruling status inside tx.
func SetCandidateStatus(tx *sql.Tx, id, status, note string) error {
	_, err := tx.Exec(`UPDATE candidates SET status=?, note=? WHERE id=?`, status, note, id)
	return err
}

// AddDependency records a derived-from link inside tx.
func AddDependency(tx *sql.Tx, d *Dependency) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO dependencies(derived_kind,derived_id,source_id,version_seq)
		VALUES(?,?,?,?)`, d.DerivedKind, d.DerivedID, d.SourceID, d.VersionSeq)
	return err
}

// CreateDiagnostic stores an import diagnostic inside tx.
func CreateDiagnostic(tx *sql.Tx, d *DiagnosticRow) error {
	if d.ID == "" {
		d.ID = NewID("diag")
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO diagnostics(id,source_id,message,raw_value,created_at)
		VALUES(?,?,?,?,?)`, d.ID, d.SourceID, d.Message, d.RawValue, d.CreatedAt)
	return err
}

// CreateCounterexample inserts a counterexample record inside tx.
func CreateCounterexample(tx *sql.Tx, c *Counterexample) error {
	if c.ID == "" {
		c.ID = NewID("cex")
	}
	if c.CreatedAt == "" {
		c.CreatedAt = now()
	}
	_, err := tx.Exec(`INSERT INTO counterexamples(id,name,scenario,description,branch_id,baseline_run_id,variant_run_id,changed_source_id,judgment,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Scenario, c.Description, c.BranchID, c.BaselineRunID,
		c.VariantRunID, c.ChangedSourceID, c.Judgment, c.CreatedAt)
	return err
}

// SetCounterexampleJudgment updates the judgment inside tx.
func SetCounterexampleJudgment(tx *sql.Tx, id, judgment string) error {
	_, err := tx.Exec(`UPDATE counterexamples SET judgment=? WHERE id=?`, judgment, id)
	return err
}

// ---- reads ----

// GetSource loads one source.
func (s *Store) GetSource(id string) (*Source, error) {
	var src Source
	err := s.db.QueryRow(`SELECT id,kind,name,raw,hash,unit,basis,created_at FROM sources WHERE id=?`, id).
		Scan(&src.ID, &src.Kind, &src.Name, &src.Raw, &src.Hash, &src.Unit, &src.Basis, &src.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// ListSources lists sources in insertion order.
func (s *Store) ListSources() ([]Source, error) {
	rows, err := s.db.Query(`SELECT id,kind,name,raw,hash,unit,basis,created_at FROM sources ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var src Source
		if err := rows.Scan(&src.ID, &src.Kind, &src.Name, &src.Raw, &src.Hash, &src.Unit, &src.Basis, &src.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// GetModelVersion loads one model version.
func (s *Store) GetModelVersion(id string) (*ModelVersion, error) {
	var mv ModelVersion
	var ids string
	err := s.db.QueryRow(`SELECT id,seq,parent_id,label,model_json,source_ids,created_at FROM model_versions WHERE id=?`, id).
		Scan(&mv.ID, &mv.Seq, &mv.ParentID, &mv.Label, &mv.ModelJSON, &ids, &mv.CreatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(ids), &mv.SourceIDs)
	return &mv, nil
}

// ListModelVersions lists all model versions by sequence.
func (s *Store) ListModelVersions() ([]ModelVersion, error) {
	rows, err := s.db.Query(`SELECT id,seq,parent_id,label,model_json,source_ids,created_at FROM model_versions ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelVersion
	for rows.Next() {
		var mv ModelVersion
		var ids string
		if err := rows.Scan(&mv.ID, &mv.Seq, &mv.ParentID, &mv.Label, &mv.ModelJSON, &ids, &mv.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ids), &mv.SourceIDs)
		out = append(out, mv)
	}
	return out, rows.Err()
}

// GetBranch loads one branch.
func (s *Store) GetBranch(id string) (*Branch, error) {
	var b Branch
	err := s.db.QueryRow(`SELECT id,name,parent_id,model_version_id,rule_version,head_seq,created_at FROM branches WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.ParentID, &b.ModelVersionID, &b.RuleVersion, &b.HeadSeq, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBranches lists branches in insertion order.
func (s *Store) ListBranches() ([]Branch, error) {
	rows, err := s.db.Query(`SELECT id,name,parent_id,model_version_id,rule_version,head_seq,created_at FROM branches ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Branch
	for rows.Next() {
		var b Branch
		if err := rows.Scan(&b.ID, &b.Name, &b.ParentID, &b.ModelVersionID, &b.RuleVersion, &b.HeadSeq, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetRun loads one run.
func (s *Store) GetRun(id string) (*Run, error) {
	var r Run
	err := s.db.QueryRow(`SELECT id,branch_id,model_version_id,rule_version,strategy,budget,status,result_json,created_at FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.BranchID, &r.ModelVersionID, &r.RuleVersion, &r.Strategy, &r.Budget, &r.Status, &r.ResultJSON, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns lists runs, optionally filtered by branch.
func (s *Store) ListRuns(branchID string) ([]Run, error) {
	q := `SELECT id,branch_id,model_version_id,rule_version,strategy,budget,status,result_json,created_at FROM runs`
	var args []any
	if branchID != "" {
		q += ` WHERE branch_id=?`
		args = append(args, branchID)
	}
	q += ` ORDER BY rowid`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.BranchID, &r.ModelVersionID, &r.RuleVersion,
			&r.Strategy, &r.Budget, &r.Status, &r.ResultJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetCandidate loads one candidate.
func (s *Store) GetCandidate(id string) (*Candidate, error) {
	var c Candidate
	err := s.db.QueryRow(`SELECT id,branch_id,run_id,kind,payload_json,status,note,created_at FROM candidates WHERE id=?`, id).
		Scan(&c.ID, &c.BranchID, &c.RunID, &c.Kind, &c.PayloadJSON, &c.Status, &c.Note, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCandidates lists candidates, optionally filtered by branch.
func (s *Store) ListCandidates(branchID string) ([]Candidate, error) {
	q := `SELECT id,branch_id,run_id,kind,payload_json,status,note,created_at FROM candidates`
	var args []any
	if branchID != "" {
		q += ` WHERE branch_id=?`
		args = append(args, branchID)
	}
	q += ` ORDER BY rowid`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ID, &c.BranchID, &c.RunID, &c.Kind, &c.PayloadJSON, &c.Status, &c.Note, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListEvents lists events in log order, optionally filtered by branch.
// upTo > 0 restricts to events with seq <= upTo (historical views).
func (s *Store) ListEvents(branchID string, upTo int64) ([]Event, error) {
	q := `SELECT seq,id,branch_id,kind,payload_json,model_version_id,rule_version,created_at FROM events WHERE 1=1`
	var args []any
	if branchID != "" {
		q += ` AND branch_id=?`
		args = append(args, branchID)
	}
	if upTo > 0 {
		q += ` AND seq<=?`
		args = append(args, upTo)
	}
	q += ` ORDER BY seq`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.ID, &e.BranchID, &e.Kind, &e.PayloadJSON,
			&e.ModelVersionID, &e.RuleVersion, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DependentsOf returns derived objects that depend on a source: the
// invalidation scope when that source changes.
func (s *Store) DependentsOf(sourceID string) ([]Dependency, error) {
	rows, err := s.db.Query(`SELECT derived_kind,derived_id,source_id,version_seq FROM dependencies WHERE source_id=? ORDER BY derived_kind,derived_id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.DerivedKind, &d.DerivedID, &d.SourceID, &d.VersionSeq); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DependenciesOf returns the sources a derived object was computed from.
func (s *Store) DependenciesOf(kind, id string) ([]Dependency, error) {
	rows, err := s.db.Query(`SELECT derived_kind,derived_id,source_id,version_seq FROM dependencies WHERE derived_kind=? AND derived_id=? ORDER BY source_id`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.DerivedKind, &d.DerivedID, &d.SourceID, &d.VersionSeq); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDiagnostics lists diagnostics, optionally filtered by source.
func (s *Store) ListDiagnostics(sourceID string) ([]DiagnosticRow, error) {
	q := `SELECT id,source_id,message,raw_value,created_at FROM diagnostics`
	var args []any
	if sourceID != "" {
		q += ` WHERE source_id=?`
		args = append(args, sourceID)
	}
	q += ` ORDER BY rowid`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiagnosticRow
	for rows.Next() {
		var d DiagnosticRow
		if err := rows.Scan(&d.ID, &d.SourceID, &d.Message, &d.RawValue, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetCounterexample loads one counterexample.
func (s *Store) GetCounterexample(id string) (*Counterexample, error) {
	var c Counterexample
	err := s.db.QueryRow(`SELECT id,name,scenario,description,branch_id,baseline_run_id,variant_run_id,changed_source_id,judgment,created_at FROM counterexamples WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Scenario, &c.Description, &c.BranchID, &c.BaselineRunID,
			&c.VariantRunID, &c.ChangedSourceID, &c.Judgment, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCounterexamples lists the archive in insertion order.
func (s *Store) ListCounterexamples() ([]Counterexample, error) {
	rows, err := s.db.Query(`SELECT id,name,scenario,description,branch_id,baseline_run_id,variant_run_id,changed_source_id,judgment,created_at FROM counterexamples ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Counterexample
	for rows.Next() {
		var c Counterexample
		if err := rows.Scan(&c.ID, &c.Name, &c.Scenario, &c.Description, &c.BranchID,
			&c.BaselineRunID, &c.VariantRunID, &c.ChangedSourceID, &c.Judgment, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConflictError reports that a write was based on a stale branch head.
// It carries both sides so the UI can show the diff.
type ConflictError struct {
	BranchID     string  `json:"branch_id"`
	BaseSeq      int64   `json:"base_seq"`
	HeadSeq      int64   `json:"head_seq"`
	ServerEvents []Event `json:"server_events"` // events committed after BaseSeq
	Attempted    string  `json:"attempted"`     // human summary of the rejected write
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("branch %s: head moved from %d to %d", e.BranchID, e.BaseSeq, e.HeadSeq)
}

// CommitEvent atomically checks the branch head, appends the event and
// moves the head. A stale baseSeq yields a ConflictError carrying the
// events the caller missed: the second of two concurrent savers sees the
// conflict instead of silently overwriting.
func (s *Store) CommitEvent(branchID string, baseSeq int64, e *Event, extra func(tx *sql.Tx) error) (*Event, error) {
	var committed *Event
	err := s.InTx(func(tx *sql.Tx) error {
		var head int64
		if err := tx.QueryRow(`SELECT head_seq FROM branches WHERE id=?`, branchID).Scan(&head); err != nil {
			return err
		}
		if head != baseSeq {
			rows, err := tx.Query(`SELECT seq,id,branch_id,kind,payload_json,model_version_id,rule_version,created_at FROM events WHERE branch_id=? AND seq>? ORDER BY seq`, branchID, baseSeq)
			if err != nil {
				return err
			}
			defer rows.Close()
			ce := &ConflictError{BranchID: branchID, BaseSeq: baseSeq, HeadSeq: head}
			for rows.Next() {
				var ev Event
				if err := rows.Scan(&ev.Seq, &ev.ID, &ev.BranchID, &ev.Kind, &ev.PayloadJSON,
					&ev.ModelVersionID, &ev.RuleVersion, &ev.CreatedAt); err != nil {
					return err
				}
				ce.ServerEvents = append(ce.ServerEvents, ev)
			}
			return ce
		}
		e.BranchID = branchID
		seq, err := AppendEvent(tx, e)
		if err != nil {
			return err
		}
		if err := SetBranchHead(tx, branchID, seq); err != nil {
			return err
		}
		if extra != nil {
			if err := extra(tx); err != nil {
				return err
			}
		}
		committed = e
		return nil
	})
	return committed, err
}

// EffectiveRulings folds the event log: a ruling applies unless a later
// undo cancels it. Undo therefore restores the previous branch state
// without rewriting history.
func EffectiveRulings(events []Event) (rulingByCandidate map[string]Event, undone map[string]bool) {
	rulingByCandidate = map[string]Event{}
	undone = map[string]bool{}
	rulingEvents := map[string]Event{} // event id -> event
	for _, e := range events {
		switch e.Kind {
		case "ruling":
			var p struct {
				CandidateID string `json:"candidate_id"`
			}
			json.Unmarshal([]byte(e.PayloadJSON), &p)
			if p.CandidateID != "" {
				rulingByCandidate[p.CandidateID] = e
				rulingEvents[e.ID] = e
			}
		case "undo":
			var p struct {
				Undoes string `json:"undoes"`
			}
			json.Unmarshal([]byte(e.PayloadJSON), &p)
			if r, ok := rulingEvents[p.Undoes]; ok {
				var rp struct {
					CandidateID string `json:"candidate_id"`
				}
				json.Unmarshal([]byte(r.PayloadJSON), &rp)
				delete(rulingByCandidate, rp.CandidateID)
				undone[p.Undoes] = true
			}
		}
	}
	return rulingByCandidate, undone
}
