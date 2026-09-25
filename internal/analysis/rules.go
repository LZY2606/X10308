// Package analysis turns versioned source documents into versioned
// interpretations and runs the derivation rules over them.
package analysis

// Rule identities are stable strings; every derived judgment event
// references the exact rule versions that produced it.
const (
	RuleEnablement        = "rule:enablement@v1" // input + inhibitor + capacity jointly
	RuleCanonicalization  = "rule:canonicalization@v1" // never merge token-distinct markings
	RuleBudgetExhaustion  = "rule:budget-exhaustion@v1" // unknown + frontier preserved
	RuleCoverability      = "rule:coverability@v1" // Karp-Miller with capacity-aware acceleration
)

// AllRules lists the rule registry for a given interpretation.
func AllRules() []string {
	return []string{RuleEnablement, RuleCanonicalization, RuleBudgetExhaustion, RuleCoverability}
}

// Aspect names: the seven versioned dimensions of an interpretation.
const (
	AspectOriginalModel      = "original_model"
	AspectNodeIdentity       = "node_identity"
	AspectArcWeights         = "arc_weights"
	AspectInitialTokens      = "initial_tokens"
	AspectCapacities         = "capacities"
	AspectExplorationStrategy = "exploration_strategy"
	AspectTruncationBudget   = "truncation_budget"
)

// AspectNames returns the aspect list in canonical order.
func AspectNames() []string {
	return []string{
		AspectOriginalModel, AspectNodeIdentity, AspectArcWeights,
		AspectInitialTokens, AspectCapacities,
		AspectExplorationStrategy, AspectTruncationBudget,
	}
}
