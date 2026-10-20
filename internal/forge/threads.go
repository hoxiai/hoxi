package forge

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

const reviewThreadsQuery = `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewThreads(first: 50) {
        nodes {
          isResolved
          isOutdated
          path
          line
          comments(first: 10) {
            nodes {
              author { login }
              body
            }
          }
        }
      }
    }
  }
}`

type reviewThreadsPayload struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool   `json:"isResolved"`
						IsOutdated bool   `json:"isOutdated"`
						Path       string `json:"path"`
						Line       int    `json:"line"`
						Comments   struct {
							Nodes []struct {
								Author struct {
									Login string `json:"login"`
								} `json:"author"`
								Body string `json:"body"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// Detail resolves the pull request and attaches unresolved review threads.
// Thread lookup failures degrade to the plain status response.
func (s *Service) Detail(ctx context.Context, directory, branch string) Response {
	response := s.Status(ctx, directory, branch)
	if !response.Available || response.PullRequest == nil {
		return response
	}
	dir, reason := resolveDirectory(directory)
	if reason != "" {
		return response
	}
	threads := s.reviewThreads(ctx, dir, *response.PullRequest)
	response.PullRequest.ReviewThreads = threads
	for _, thread := range threads {
		if !thread.IsResolved {
			response.PullRequest.UnresolvedThreads++
		}
	}
	return response
}

func (s *Service) reviewThreads(ctx context.Context, dir string, pr PullRequest) []ReviewThread {
	owner, repo, ok := repoFromPullRequestURL(pr.URL)
	if !ok {
		return nil
	}
	result := s.runGH(ctx, dir,
		"api", "graphql",
		"-f", "query="+reviewThreadsQuery,
		"-f", "owner="+owner,
		"-f", "repo="+repo,
		"-F", "number="+strconv.Itoa(pr.Number),
	)
	if result.err != nil {
		return nil
	}

	var payload reviewThreadsPayload
	if err := json.Unmarshal(result.stdout, &payload); err != nil {
		return nil
	}

	nodes := payload.Data.Repository.PullRequest.ReviewThreads.Nodes
	threads := make([]ReviewThread, 0, len(nodes))
	for _, node := range nodes {
		comments := make([]ReviewComment, 0, len(node.Comments.Nodes))
		for _, comment := range node.Comments.Nodes {
			body := clipRunes(strings.TrimSpace(comment.Body), 1200)
			if body == "" {
				continue
			}
			author := strings.TrimSpace(comment.Author.Login)
			if author == "" {
				author = "reviewer"
			}
			comments = append(comments, ReviewComment{Author: author, Body: body})
		}
		if len(comments) == 0 {
			continue
		}
		threads = append(threads, ReviewThread{
			Path:       strings.TrimSpace(node.Path),
			Line:       node.Line,
			IsResolved: node.IsResolved,
			IsOutdated: node.IsOutdated,
			Comments:   comments,
		})
	}
	return threads
}

func repoFromPullRequestURL(raw string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func clipRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
