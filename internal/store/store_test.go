package store

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedBranch(t *testing.T, s *Store) *Branch {
	t.Helper()
	b := &Branch{ID: "br-test", Name: "test", ModelVersionID: "mv-0", RuleVersion: 2}
	if err := s.InTx(func(tx *sql.Tx) error {
		return CreateBranch(tx, b)
	}); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConflictOnConcurrentSave(t *testing.T) {
	s := openTemp(t)
	b := seedBranch(t, s)

	// Window A commits at base 0.
	if _, err := s.CommitEvent(b.ID, 0, &Event{Kind: "ruling", PayloadJSON: `{"candidate_id":"c1","decision":"accepted"}`}, nil); err != nil {
		t.Fatal(err)
	}
	// Window B still holds base 0: must see a conflict with both sides.
	_, err := s.CommitEvent(b.ID, 0, &Event{Kind: "ruling", PayloadJSON: `{"candidate_id":"c2","decision":"rejected"}`}, nil)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if ce.BaseSeq != 0 || ce.HeadSeq != 1 || len(ce.ServerEvents) != 1 {
		t.Fatalf("conflict must carry base, head and missed events: %+v", ce)
	}
	// Retrying with the fresh head succeeds.
	if _, err := s.CommitEvent(b.ID, ce.HeadSeq, &Event{Kind: "ruling", PayloadJSON: `{"candidate_id":"c2","decision":"rejected"}`}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUndoRestoresPreviousBranch(t *testing.T) {
	s := openTemp(t)
	b := seedBranch(t, s)
	r1, err := s.CommitEvent(b.ID, 0, &Event{Kind: "ruling", PayloadJSON: `{"candidate_id":"c1","decision":"accepted"}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitEvent(b.ID, r1.Seq, &Event{Kind: "undo", PayloadJSON: `{"undoes":"` + r1.ID + `"}`}, nil); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents(b.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rulings, undone := EffectiveRulings(events)
	if len(rulings) != 0 {
		t.Fatalf("undo must cancel the ruling, got %+v", rulings)
	}
	if !undone[r1.ID] {
		t.Fatalf("undo must be recorded against the ruling event")
	}
	// Historical view at seq=1 still shows the ruling: history is not
	// overwritten by the undo.
	atOne, _ := s.ListEvents(b.ID, r1.Seq)
	r1s, _ := EffectiveRulings(atOne)
	if len(r1s) != 1 {
		t.Fatalf("view at event %d must restore the then-visible ruling", r1.Seq)
	}
}

func TestFailedImportRollsBack(t *testing.T) {
	s := openTemp(t)
	err := s.InTx(func(tx *sql.Tx) error {
		if err := CreateSource(tx, &Source{ID: "src-a", Kind: "model", Name: "a", Raw: []byte("{}")}); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	srcs, _ := s.ListSources()
	if len(srcs) != 0 {
		t.Fatalf("failed import must not leave half a record: %+v", srcs)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	s := openTemp(t)
	b := seedBranch(t, s)
	if err := s.InTx(func(tx *sql.Tx) error {
		if err := CreateSource(tx, &Source{ID: "src-1", Kind: "model", Name: "m", Raw: []byte(`{"a":1}`), Unit: "token", Basis: "px"}); err != nil {
			return err
		}
		if err := CreateModelVersion(tx, &ModelVersion{ID: "mv-1", Seq: 1, Label: "v1", ModelJSON: "{}", SourceIDs: []string{"src-1"}}); err != nil {
			return err
		}
		if err := CreateRun(tx, &Run{ID: "run-1", BranchID: b.ID, ModelVersionID: "mv-1", RuleVersion: 2, Strategy: "bfs", Budget: 8, Status: "unknown", ResultJSON: `{"frontier":["s3"]}`}); err != nil {
			return err
		}
		if err := CreateCandidate(tx, &Candidate{ID: "cand-1", BranchID: b.ID, RunID: "run-1", Kind: "deadlock", PayloadJSON: `{"state":"s2"}`}); err != nil {
			return err
		}
		if err := AddDependency(tx, &Dependency{DerivedKind: "run", DerivedID: "run-1", SourceID: "src-1", VersionSeq: 1}); err != nil {
			return err
		}
		if err := CreateDiagnostic(tx, &DiagnosticRow{ID: "diag-1", SourceID: "src-1", Message: "m", RawValue: "unknown"}); err != nil {
			return err
		}
		return CreateCounterexample(tx, &Counterexample{ID: "cex-1", Name: "n", Scenario: "self-loop", BranchID: b.ID, BaselineRunID: "run-1", VariantRunID: "run-1", ChangedSourceID: "src-1", Judgment: "accepted"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitEvent(b.ID, 0, &Event{ID: "ev-1", Kind: "ruling", PayloadJSON: `{"candidate_id":"cand-1","decision":"accepted"}`}, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := s.Export(&buf); err != nil {
		t.Fatal("export1:", err)
	}
	exported := append([]byte(nil), buf.Bytes()...)

	// Delete the local database, reimport into a fresh one.
	s2 := openTemp(t)
	if err := s2.Import(bytes.NewReader(exported)); err != nil {
		t.Fatal(err)
	}
	var buf2 bytes.Buffer
	if err := s2.Export(&buf2); err != nil {
		t.Fatal(err)
	}
	// Identity, order and provenance must survive the round trip.
	if !bytes.Equal(exported, buf2.Bytes()) {
		a, b := string(exported), buf2.String()
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				lo := i - 120
				if lo < 0 {
					lo = 0
				}
				t.Fatalf("re-export differs at %d:\nA: %s\nB: %s", i, a[lo:i+120], b[lo:i+120])
			}
		}
		t.Fatalf("re-export differs in length %d vs %d", len(a), len(b))
	}
	src, err := s2.GetSource("src-1")
	if err != nil || src.Hash != Hash([]byte(`{"a":1}`)) {
		t.Fatalf("source identity lost: %+v %v", src, err)
	}
	deps, _ := s2.DependentsOf("src-1")
	if len(deps) != 1 || deps[0].DerivedID != "run-1" {
		t.Fatalf("provenance lost: %+v", deps)
	}
	events, _ := s2.ListEvents("", 0)
	if len(events) != 1 || events[0].ID != "ev-1" || events[0].Seq != 1 {
		t.Fatalf("event order/identity lost: %+v", events)
	}
	// Import into a non-empty database must fail without changes.
	if err := s2.Import(strings.NewReader(buf2.String())); err == nil {
		t.Fatalf("import into non-empty db must fail")
	}
}

