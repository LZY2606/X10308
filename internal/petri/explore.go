package petri

// StateView is one reached state-space node.
type StateView struct {
	ID       string `json:"id"`
	Marking  string `json:"marking"`
	Depth    int    `json:"depth"`
	Deadlock bool   `json:"deadlock"`
}

// EdgeView is one fired transition between states.
type EdgeView struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
	Arc        string `json:"arc,omitempty"`
}

// FrontierItem is retained work when the budget is exhausted.
type FrontierItem struct {
	StateID    string `json:"state_id"`
	Marking    string `json:"marking"`
	Depth      int    `json:"depth"`
	FromState  string `json:"from_state,omitempty"`
	Transition string `json:"transition,omitempty"`
	Reason     string `json:"reason"`
}

// RuleEvent is a machine-readable judgement. IDs are stable across replay.
type RuleEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Transition string         `json:"transition,omitempty"`
	StateID    string         `json:"state_id,omitempty"`
	Marking    string         `json:"marking,omitempty"`
	Verdict    string         `json:"verdict"`
	Basis      map[string]any `json:"basis"`
}

// Candidate is one legal path/counterexample retained for adjudication.
type Candidate struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	StateID   string   `json:"state_id"`
	Marking   string   `json:"marking"`
	Path      []string `json:"path"`
	Depth     int      `json:"depth"`
	Accepted  *bool    `json:"accepted,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// Result is the complete derived exploration outcome.
type Result struct {
	RunID      string         `json:"run_id"`
	Strategy   string         `json:"strategy"`
	Budget     int            `json:"budget"`
	Status     string         `json:"status"` // reachable | not-reachable | unknown | invalid-input
	Reachable  *bool          `json:"reachable,omitempty"`
	Target     string         `json:"target,omitempty"`
	States     []StateView    `json:"states"`
	Edges      []EdgeView     `json:"edges"`
	Covers     []CoverPair    `json:"covers"`
	Deadlocks  []StateView    `json:"deadlocks"`
	Frontier   []FrontierItem `json:"frontier"`
	RuleEvents []RuleEvent    `json:"rule_events"`
	Candidates []Candidate    `json:"candidates"`
	Promotions []Promotion    `json:"promotions"`
	Unbounded  bool           `json:"unbounded"`
	Diagnostics []Diagnostic  `json:"diagnostics"`
	BudgetExhausted bool       `json:"budget_exhausted"`
}

// CoverPair records state A covering marking B (component-wise >=).
type CoverPair struct {
	Covering string `json:"covering"`
	Covered  string `json:"covered"`
}

// Promotion records one Karp-Miller omega introduction.
type Promotion struct {
	Place      string `json:"place"`
	Ancestor   string `json:"ancestor"`
	Current    string `json:"current"`
	OmegaAfter string `json:"omega_after"`
}
