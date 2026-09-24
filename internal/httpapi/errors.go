package httpapi

import "errors"

var (
	errMethod         = errors.New("method not allowed")
	errBranchRequired = errors.New("branch_id query parameter is required")
	errNotFound       = errors.New("not found")
	errNoCandidate    = errors.New("branch has no candidate yet; load the case pack first")
)
