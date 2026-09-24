package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scanCounterexample(row interface{ Scan(...any) error }) (CounterexampleRow, error) {
	var c CounterexampleRow
	var detail string
	var decidedEvent int64
	err := row.Scan(&c.ID, &c.Seq, &c.BranchID, &c.RunID, &c.Scenario,
		&c.Title, &detail, &c.Verdict, &c.Rev, &decidedEvent, &c.DecidedBy)
	if err != nil {
		return c, err
	}
	c.Detail = json.RawMessage(detail)
	if decidedEvent > 0 {
		c.DecidedEvent = &decidedEvent
	}
	return c, nil
}

var _ = sql.NullString{}
