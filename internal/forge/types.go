package forge

// Check is one CI check attached to a pull request.
type Check struct {
	Name  string `json:"name"`
	State string `json:"state"` // success | failure | pending | neutral
	URL   string `json:"url,omitempty"`
}

// ReviewComment is one comment inside a pull request review thread.
type ReviewComment struct {
	Author string `json:"author,omitempty"`
	Body   string `json:"body"`
}

// ReviewThread is one review conversation anchored to a file and line.
type ReviewThread struct {
	Path       string          `json:"path"`
	Line       int             `json:"line"`
	IsResolved bool            `json:"isResolved"`
	IsOutdated bool            `json:"isOutdated"`
	Comments   []ReviewComment `json:"comments"`
}

// PullRequest is the normalized subset Hoxi needs to badge a worktree branch.
type PullRequest struct {
	Number            int            `json:"number"`
	URL               string         `json:"url"`
	Title             string         `json:"title"`
	State             string         `json:"state"`
	IsDraft           bool           `json:"isDraft"`
	ReviewDecision    string         `json:"reviewDecision,omitempty"`
	MergeStateStatus  string         `json:"mergeStateStatus,omitempty"`
	Mergeable         string         `json:"mergeable,omitempty"`
	Additions         int            `json:"additions"`
	Deletions         int            `json:"deletions"`
	ChangedFiles      int            `json:"changedFiles"`
	BaseRefName       string         `json:"baseRefName,omitempty"`
	HeadRefName       string         `json:"headRefName,omitempty"`
	Checks            []Check        `json:"checks"`
	ChecksPassed      int            `json:"checksPassed"`
	ChecksFailed      int            `json:"checksFailed"`
	ChecksPending     int            `json:"checksPending"`
	ChecksTotal       int            `json:"checksTotal"`
	ChecksState       string         `json:"checksState"` // success | failure | pending | none
	UnresolvedThreads int            `json:"unresolvedThreads"`
	ReviewThreads     []ReviewThread `json:"reviewThreads,omitempty"`
}

// Response reports whether PR status is available for a branch.
type Response struct {
	Available   bool         `json:"available"`
	Reason      string       `json:"reason,omitempty"`
	Branch      string       `json:"branch,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
}

// ListResponse maps branches to their most relevant pull request.
type ListResponse struct {
	Available    bool                   `json:"available"`
	Reason       string                 `json:"reason,omitempty"`
	PullRequests map[string]PullRequest `json:"pullRequests"`
}

// CreateRequest is the payload for creating a pull request through `gh pr create`.
type CreateRequest struct {
	Directory string `json:"directory"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Base      string `json:"base,omitempty"`
	Head      string `json:"head,omitempty"`
	Draft     bool   `json:"draft,omitempty"`
}

// CreateResponse reports the outcome of a pull request creation request.
type CreateResponse struct {
	OK          bool         `json:"ok"`
	Available   bool         `json:"available"`
	Reason      string       `json:"reason,omitempty"`
	Error       string       `json:"error,omitempty"`
	URL         string       `json:"url,omitempty"`
	Branch      string       `json:"branch,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
}
