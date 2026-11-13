package workcatalog

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type syncRun struct {
	ID       string             `json:"id"`
	NextPage int                `json:"nextPage"`
	Since    time.Time          `json:"since"`
	Until    time.Time          `json:"until"`
	Products map[string]Product `json:"products"`
}

type SyncInput struct {
	Restart  bool   `json:"restart,omitempty" jsonschema:"Start a fresh sync, replacing any unfinished cursor"`
	Since    string `json:"since,omitempty" jsonschema:"Optional RFC3339 modification time for incremental sync; omit for a full snapshot"`
	MaxPages int    `json:"maxPages,omitempty" jsonschema:"Pages to fetch in this call, 1..10; default 1; call again to resume"`
}

type SyncResult struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Fetched    int       `json:"fetched"`
	Total      int       `json:"total"`
	NextPage   int       `json:"nextPage,omitempty"`
	Since      time.Time `json:"since"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

type syncJobContextKey struct{}

// Sync stages full pages without changing the visible snapshot. Only a complete
// traversal swaps it in. A failed request resumes from its last persisted page.
func (s *Service) Sync(ctx context.Context, scope Scope, api Platform, in SyncInput) (SyncResult, error) {
	if ctx.Value(syncJobContextKey{}) != true {
		unlock, err := s.store.lock(ctx, scope, ".job.lock")
		if err != nil {
			return SyncResult{}, err
		}
		defer unlock()
	}
	if in.MaxPages == 0 {
		in.MaxPages = 1
	}
	if in.MaxPages < 1 || in.MaxPages > 10 {
		return SyncResult{}, errors.New("maxPages must be 1..10")
	}
	var since time.Time
	if in.Since != "" {
		var err error
		since, err = time.Parse(time.RFC3339, in.Since)
		if err != nil || since.After(time.Now()) {
			return SyncResult{}, errors.New("since must be an RFC3339 time in the past")
		}
	}
	unlock, err := s.store.lock(ctx, scope, ".operation.lock")
	if err != nil {
		return SyncResult{}, err
	}
	defer unlock()
	if err := s.store.update(ctx, scope, func(st *state) error {
		if st.Sync != nil && !in.Restart {
			if in.Since != "" && !since.Equal(st.Sync.Since) {
				return errors.New("unfinished sync uses another time range; resume without since or set restart")
			}
			return nil
		}
		id, err := newID("syn_")
		if err != nil {
			return err
		}
		st.Sync = &syncRun{ID: id, NextPage: 1, Since: since, Until: time.Now().UTC(), Products: map[string]Product{}}
		return nil
	}); err != nil {
		return SyncResult{}, err
	}
	var result SyncResult
	for range in.MaxPages {
		st, err := s.store.load(ctx, scope)
		if err != nil {
			return result, err
		}
		run := st.Sync
		page, err := api.List(ctx, run.NextPage, run.Since, run.Until)
		if err != nil {
			return result, err
		}
		if page.Total < 0 || page.Total > 100000 || len(page.Products) > 20 {
			return result, errors.New("invalid platform pagination response")
		}
		result = SyncResult{ID: run.ID, Status: "running", Total: page.Total, Since: run.Since, StartedAt: run.Until}
		err = s.store.update(ctx, scope, func(st *state) error {
			for _, p := range page.Products {
				if err := validateOwnedProduct(p, scope.SellerLoginID); err != nil {
					return err
				}
				if _, exists := st.Sync.Products[p.ID]; exists {
					return fmt.Errorf("duplicate product %s during pagination; restart the sync", p.ID)
				}
				st.Sync.Products[p.ID] = p
			}
			result.Fetched = len(st.Sync.Products)
			if result.Fetched > page.Total || len(page.Products) == 0 && result.Fetched < page.Total {
				return errors.New("inconsistent platform product count; restart the sync")
			}
			if result.Fetched == page.Total {
				if run.Since.IsZero() {
					st.Products = st.Sync.Products
				} else {
					for id, p := range st.Sync.Products {
						st.Products[id] = p
					}
				}
				result.Status, result.FinishedAt = "completed", time.Now().UTC()
				st.LastSync, st.Sync = &result, nil
			} else {
				st.Sync.NextPage++
				result.NextPage = st.Sync.NextPage
			}
			return nil
		})
		if err != nil || result.Status == "completed" {
			return result, err
		}
	}
	return result, nil
}

func validateOwnedProduct(p Product, seller string) error {
	if !productIDPattern.MatchString(p.ID) || p.Data == nil || scalarID(p.Data["productID"]) != p.ID {
		return errors.New("invalid platform product ID")
	}
	owner, _ := p.Data["sellerLoginId"].(string)
	if owner == "" {
		owner, _ = p.Data["supplierLoginId"].(string)
	}
	if owner == "" || owner != seller {
		return errors.New("platform product seller does not match the bound store")
	}
	return nil
}
