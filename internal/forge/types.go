package forge

// Check is one CI check attached to a pull request.
type Check struct {
	Name  string `json:"name"`
	State string `json:"state"` // success | failure | pending | neutral
	URL   string `json:"url,omitempty"`
}

// PullRequest is the normalized subset Hoxi needs to badge a worktree branch.
type PullRequest struct {
	Number           int     `json:"number"`
	URL              string  `json:"url"`
	Title            string  `json:"title"`
	State            string  `json:"state"`
	IsDraft          bool    `json:"isDraft"`
	ReviewDecision   string  `json:"reviewDecision,omitempty"`
	MergeStateStatus string  `json:"mergeStateStatus,omitempty"`
	Mergeable        string  `json:"mergeable,omitempty"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
	ChangedFiles     int     `json:"changedFiles"`
	BaseRefName      string  `json:"baseRefName,omitempty"`
	HeadRefName      string  `json:"headRefName,omitempty"`
	Checks           []Check `json:"checks"`
	ChecksPassed     int     `json:"checksPassed"`
	ChecksFailed     int     `json:"checksFailed"`
	ChecksPending    int     `json:"checksPending"`
	ChecksTotal      int     `json:"checksTotal"`
	ChecksState      string  `json:"checksState"` // success | failure | pending | none
}

// Response reports whether PR status is available for a branch.
type Response struct {
	Available   bool         `json:"available"`
	Reason      string       `json:"reason,omitempty"`
	Branch      string       `json:"branch,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
}
