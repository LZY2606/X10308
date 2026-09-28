// Package rules is the single versioned source of truth for the derivation
// rules. Every computed result records RulesVersion so a historical event can
// always be interpreted under the rules that produced it.
package rules

// Version is bumped whenever the enablement, canonicalization or budget
// semantics change. Old events keep the version they were produced under.
const Version = "rules-2026.09-tokenflow-1"

// Clause identifies a machine-readable judgment rule referenced by events.
type Clause string

const (
	// ClauseEnableInputs requires every input arc to have enough tokens.
	ClauseEnableInputs Clause = "enable.input"
	// ClauseEnableInhibitor requires every inhibitor place to hold fewer
	// tokens than its arc weight.
	ClauseEnableInhibitor Clause = "enable.inhibitor"
	// ClauseEnableCapacity requires the post-fire marking to respect place
	// capacities.
	ClauseEnableCapacity Clause = "enable.capacity"
	// ClauseCanonicalTokens states that canonicalization keys exact token
	// counts and never mergges markings that differ.
	ClauseCanonicalTokens Clause = "canonical.tokens"
	// ClauseBudgetUnknown states that budget exhaustion yields UNKNOWN and
	// retains the unexplored frontier.
	ClauseBudgetUnknown Clause = "budget.unknown"
	// ClauseCoverOmega states that omega components mark strictly dominated,
	// unbounded places and never match a concrete target.
	ClauseCoverOmega Clause = "cover.omega"
)

// Text returns the canonical, machine-readable statement of a clause.
func Text(c Clause) string {
	switch c {
	case ClauseEnableInputs:
		return "A transition is enabled only when every input place holds at least the arc weight."
	case ClauseEnableInhibitor:
		return "A transition is disabled when an inhibitor place holds at least its inhibitor weight."
	case ClauseEnableCapacity:
		return "A transition is enabled only when the consume-then-produce marking respects every place capacity."
	case ClauseCanonicalTokens:
		return "Markings are canonicalized by exact per-place token counts; markings with different tokens are never merged."
	case ClauseBudgetUnknown:
		return "When the node budget is exhausted before reaching a target or closing the space, the verdict is UNKNOWN and the frontier is retained."
	case ClauseCoverOmega:
		return "A successor strictly dominating an ancestor promotes dominated places to omega; omega never equals a concrete target marking."
	default:
		return "unknown clause"
	}
}

// AllClauses lists every active clause in stable order.
func AllClauses() []Clause {
	return []Clause{
		ClauseEnableInputs,
		ClauseEnableInhibitor,
		ClauseEnableCapacity,
		ClauseCanonicalTokens,
		ClauseBudgetUnknown,
		ClauseCoverOmega,
	}
}
