package workcatalog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

type SyncJob struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Fetched    int        `json:"fetched"`
	Total      int        `json:"total"`
	Message    string     `json:"message,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type syncWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Service) StartSync(ctx context.Context, target Target, in SyncInput) (SyncJob, error) {
	if in.MaxPages == 0 {
		in.MaxPages = 1
	}
	if in.MaxPages < 1 || in.MaxPages > 10 {
		return SyncJob{}, errors.New("maxPages must be 1..10")
	}
	if in.Since != "" {
		since, err := time.Parse(time.RFC3339, in.Since)
		if err != nil || since.After(time.Now()) {
			return SyncJob{}, errors.New("since must be an RFC3339 time in the past")
		}
	}
	if !target.Capabilities.Sync || !target.Authorization.Configured {
		return SyncJob{}, errors.New("configure store authorization before syncing")
	}
	api, err := target.Platform()
	if err != nil {
		return SyncJob{}, err
	}
	jobID, err := newID("job_")
	if err != nil {
		return SyncJob{}, err
	}
	// The lease is held for the whole job, while state locks are only held for
	// individual atomic updates. A second desktop process cannot start a clone.
	unlock, err := s.store.lock(ctx, target.Scope, ".job.lock")
	if err != nil {
		return SyncJob{}, err
	}
	workerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	workerCtx = context.WithValue(workerCtx, syncJobContextKey{}, true)
	worker := &syncWorker{cancel: cancel, done: make(chan struct{})}
	s.jobsMu.Lock()
	if s.jobs[target.Scope.StoreID] != nil {
		s.jobsMu.Unlock()
		cancel()
		unlock()
		return SyncJob{}, errors.New("store is busy; retry after the current operation")
	}
	if s.closed || len(s.jobs) >= 32 {
		s.jobsMu.Unlock()
		cancel()
		unlock()
		return SyncJob{}, errors.New("catalog worker is unavailable or its active-job limit was reached")
	}
	s.jobs[target.Scope.StoreID] = worker
	s.jobsMu.Unlock()
	job := SyncJob{ID: jobID, Status: "running", StartedAt: time.Now().UTC()}
	err = s.store.update(ctx, target.Scope, func(st *state) error { st.SyncJob = &job; return nil })
	if err != nil {
		s.jobsMu.Lock()
		if s.jobs[target.Scope.StoreID] == worker {
			delete(s.jobs, target.Scope.StoreID)
		}
		s.jobsMu.Unlock()
		cancel()
		unlock()
		close(worker.done)
		return SyncJob{}, err
	}
	go func() {
		defer func() {
			cancel()
			s.jobsMu.Lock()
			if s.jobs[target.Scope.StoreID] == worker {
				delete(s.jobs, target.Scope.StoreID)
			}
			s.jobsMu.Unlock()
			unlock()
			close(worker.done)
		}()
		input := in
		for {
			result, syncErr := s.Sync(workerCtx, target.Scope, api, input)
			input.Restart, input.Since = false, ""
			status, message := "running", ""
			if syncErr != nil {
				status, message = "error", "Sync failed; the saved cursor can be resumed"
				if workerCtx.Err() != nil {
					status, message = "cancelled", "Sync stopped; the saved cursor can be resumed"
				}
			} else if result.Status == "completed" {
				status = "completed"
			}
			persistCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			persistErr := s.store.update(persistCtx, target.Scope, func(st *state) error {
				current := st.SyncJob
				if current == nil || current.ID != job.ID {
					return errors.New("sync job was replaced")
				}
				current.Status, current.Message = status, message
				if syncErr == nil {
					current.Fetched, current.Total = result.Fetched, result.Total
				}
				if status != "running" {
					now := time.Now().UTC()
					current.FinishedAt = &now
				}
				return nil
			})
			stop()
			if syncErr != nil || persistErr != nil || status == "completed" {
				return
			}
		}
	}()
	return job, nil
}

func (s *Service) SyncJobStatus(ctx context.Context, scope Scope) (*SyncJob, error) {
	st, err := s.store.load(ctx, scope)
	if err != nil || st.SyncJob == nil {
		return nil, err
	}
	return s.syncJobView(scope, st.SyncJob)
}

func (s *Service) syncJobView(scope Scope, stored *SyncJob) (*SyncJob, error) {
	if stored == nil {
		return nil, nil
	}
	job := *stored
	if job.Status == "running" {
		path, err := s.store.path(scope)
		if err != nil {
			return nil, err
		}
		lease := flock.New(path + ".job.lock")
		free, err := lease.TryLock()
		_ = lease.Close()
		if err != nil {
			return nil, err
		}
		if free {
			job.Status, job.Message = "interrupted", "The previous process stopped; resume the saved cursor"
		}
	}
	return &job, nil
}

func (s *Service) CancelSync(storeID string) error {
	s.jobsMu.Lock()
	worker := s.jobs[storeID]
	if worker != nil {
		worker.cancel()
	}
	s.jobsMu.Unlock()
	if worker == nil {
		return errors.New("no sync job is running in this desktop process")
	}
	return nil
}

// Close cancels only catalog workers owned by this service, and waits for their
// receipts to persist before desktop shutdown.
func (s *Service) Close() {
	s.jobsMu.Lock()
	s.closed = true
	workers := make([]*syncWorker, 0, len(s.jobs))
	for _, worker := range s.jobs {
		worker.cancel()
		workers = append(workers, worker)
	}
	s.jobsMu.Unlock()
	var done sync.WaitGroup
	for _, worker := range workers {
		done.Add(1)
		go func() { defer done.Done(); <-worker.done }()
	}
	done.Wait()
}
