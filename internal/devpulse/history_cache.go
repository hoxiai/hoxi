package devpulse

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type historyCacheEntry struct {
	at        time.Time
	sourceKey SourceKey
	res       HistoryResponse
}

type gitHistoryDayStat struct {
	Commits   int
	Additions int
	Deletions int
	Files     int
}

type gitHistoryCacheEntry struct {
	at   time.Time
	days map[string]gitHistoryDayStat
}

type historyFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	res     HistoryResponse
	err     error
}

type historyQuery struct {
	start        time.Time
	end          time.Time
	endExclusive time.Time
	todayStart   time.Time
	days         int
	dir          string
	sessionID    string
	includeTools bool
	forced       bool
}

func cloneHistoryResponse(src HistoryResponse) HistoryResponse {
	dst := src
	dst.Days = make([]HistoryDay, len(src.Days))
	for i := range src.Days {
		dst.Days[i] = src.Days[i]
		if len(src.Days[i].Models) > 0 {
			models := make([]HistoryModelStat, len(src.Days[i].Models))
			copy(models, src.Days[i].Models)
			dst.Days[i].Models = models
		} else {
			dst.Days[i].Models = []HistoryModelStat{}
		}
	}
	return dst
}

func (s *Service) getOrComputeHistory(ctx context.Context, q historyQuery) (HistoryResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	targets := s.ResolveGitTargets(q.dir, q.sessionID)
	targetsKey := TargetsCacheKey(targets)
	var sourceKey SourceKey
	if s.source != nil {
		sourceKey = s.source.SourceKey()
	}

	includesToday := !q.todayStart.Before(q.start) && q.todayStart.Before(q.endExclusive)
	ttl := historyPastCacheTTL
	if includesToday {
		ttl = historyCacheTTL
	}

	cacheKey := fmt.Sprintf(
		"%s|%s|%s|%s|%t|%s",
		q.start.Format("2006-01-02"),
		q.end.Format("2006-01-02"),
		q.todayStart.Format("2006-01-02"),
		q.start.Location().String(),
		q.includeTools,
		targetsKey,
	)

	now := time.Now()
	s.mu.Lock()
	if s.historyEntries == nil {
		s.historyEntries = make(map[string]historyCacheEntry)
	}
	if s.historyInflight == nil {
		s.historyInflight = make(map[string]*historyFlight)
	}
	if !q.forced {
		if entry, ok := s.historyEntries[cacheKey]; ok &&
			!entry.at.IsZero() &&
			entry.sourceKey == sourceKey &&
			now.Sub(entry.at) < ttl {
			res := cloneHistoryResponse(entry.res)
			s.mu.Unlock()
			return res, nil
		}
		if flight, ok := s.historyInflight[cacheKey]; ok {
			flight.waiters++
			s.mu.Unlock()
			return s.waitHistoryFlight(ctx, cacheKey, flight)
		}
	}
	flightCtx, flightCancel := context.WithCancel(context.Background())
	flight := &historyFlight{
		done:    make(chan struct{}),
		cancel:  flightCancel,
		waiters: 1,
	}
	if !q.forced {
		s.historyInflight[cacheKey] = flight
	}
	s.mu.Unlock()

	go func() {
		defer flightCancel()
		res, cacheable, err := s.computeHistory(flightCtx, q, targets, targetsKey)
		flight.res = res
		flight.err = err

		s.mu.Lock()
		if !q.forced && s.historyInflight[cacheKey] == flight {
			delete(s.historyInflight, cacheKey)
		}
		if err == nil && cacheable && flightCtx.Err() == nil {
			s.storeHistoryCacheLocked(cacheKey, historyCacheEntry{
				at:        time.Now(),
				sourceKey: sourceKey,
				res:       cloneHistoryResponse(res),
			})
		}
		close(flight.done)
		s.mu.Unlock()
	}()

	return s.waitHistoryFlight(ctx, cacheKey, flight)
}

func (s *Service) waitHistoryFlight(ctx context.Context, cacheKey string, flight *historyFlight) (HistoryResponse, error) {
	select {
	case <-flight.done:
		if flight.err != nil {
			return HistoryResponse{}, flight.err
		}
		return cloneHistoryResponse(flight.res), nil
	case <-ctx.Done():
		s.mu.Lock()
		flight.waiters--
		shouldCancel := flight.waiters <= 0
		if shouldCancel && s.historyInflight[cacheKey] == flight {
			delete(s.historyInflight, cacheKey)
		}
		s.mu.Unlock()
		if shouldCancel {
			flight.cancel()
		}
		return HistoryResponse{}, ctx.Err()
	}
}

func (s *Service) storeHistoryCacheLocked(cacheKey string, entry historyCacheEntry) {
	if s.historyEntries == nil {
		s.historyEntries = make(map[string]historyCacheEntry)
	}
	if len(s.historyEntries) >= maxHistoryCacheEntries {
		var oldestKey string
		var oldestAt time.Time
		now := time.Now()
		for k, v := range s.historyEntries {
			if now.Sub(v.at) >= historyPastCacheTTL {
				delete(s.historyEntries, k)
				continue
			}
			if oldestKey == "" || v.at.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.at
			}
		}
		if len(s.historyEntries) >= maxHistoryCacheEntries && oldestKey != "" {
			delete(s.historyEntries, oldestKey)
		}
	}
	s.historyEntries[cacheKey] = entry
}

func (s *Service) collectGitHistoryCached(
	ctx context.Context,
	targets []GitTarget,
	targetsKey string,
	start, endExclusive, todayStart time.Time,
	forced bool,
) map[string]gitHistoryDayStat {
	if len(targets) == 0 || s.source == nil {
		return nil
	}
	gitKey := fmt.Sprintf(
		"%s|%s|%s|%s|%s",
		start.Format("2006-01-02"),
		endExclusive.Format("2006-01-02"),
		todayStart.Format("2006-01-02"),
		start.Location().String(),
		targetsKey,
	)
	now := time.Now()
	if !forced {
		s.mu.Lock()
		if entry, ok := s.gitHistoryEntries[gitKey]; ok && !entry.at.IsZero() && now.Sub(entry.at) < gitHistoryCacheTTL {
			out := make(map[string]gitHistoryDayStat, len(entry.days))
			for k, v := range entry.days {
				out[k] = v
			}
			s.mu.Unlock()
			return out
		}
		s.mu.Unlock()
	}

	localByDate := make(map[string]*HistoryDay)
	localDay := func(key string) *HistoryDay {
		if d, ok := localByDate[key]; ok {
			return d
		}
		d := &HistoryDay{Date: key}
		localByDate[key] = d
		return d
	}
	s.CollectGitHistoryCtx(ctx, targets, start, endExclusive, todayStart, localDay)
	if ctx.Err() != nil {
		return nil
	}

	out := make(map[string]gitHistoryDayStat, len(localByDate))
	for k, d := range localByDate {
		out[k] = gitHistoryDayStat{
			Commits:   d.Commits,
			Additions: d.Additions,
			Deletions: d.Deletions,
			Files:     d.Files,
		}
	}

	s.mu.Lock()
	if s.gitHistoryEntries == nil {
		s.gitHistoryEntries = make(map[string]gitHistoryCacheEntry)
	}
	if len(s.gitHistoryEntries) >= maxHistoryCacheEntries {
		var oldestKey string
		var oldestAt time.Time
		for k, v := range s.gitHistoryEntries {
			if now.Sub(v.at) >= gitHistoryCacheTTL {
				delete(s.gitHistoryEntries, k)
				continue
			}
			if oldestKey == "" || v.at.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.at
			}
		}
		if len(s.gitHistoryEntries) >= maxHistoryCacheEntries && oldestKey != "" {
			delete(s.gitHistoryEntries, oldestKey)
		}
	}
	copied := make(map[string]gitHistoryDayStat, len(out))
	for k, v := range out {
		copied[k] = v
	}
	s.gitHistoryEntries[gitKey] = gitHistoryCacheEntry{at: time.Now(), days: copied}
	s.mu.Unlock()
	return out
}

func mergeHistoryMaps(dst map[string]*HistoryDay, src map[string]*HistoryDay) {
	day := func(key string) *HistoryDay {
		if d, ok := dst[key]; ok {
			return d
		}
		d := &HistoryDay{Date: key, Models: []HistoryModelStat{}}
		dst[key] = d
		return d
	}
	for key, item := range src {
		target := day(key)
		target.Turns += item.Turns
		target.Sessions += item.Sessions
		target.InputTokens += item.InputTokens
		target.OutputTokens += item.OutputTokens
		target.ReasoningTokens += item.ReasoningTokens
		target.CacheReadTokens += item.CacheReadTokens
		target.CacheWriteTokens += item.CacheWriteTokens
		target.ToolCallsTotal += item.ToolCallsTotal
		target.ToolCallsFailed += item.ToolCallsFailed
		target.Commits += item.Commits
		target.Additions += item.Additions
		target.Deletions += item.Deletions
		target.Files += item.Files
		for _, m := range item.Models {
			mergeHistoryModel(target, m)
		}
	}
}

func (s *Service) computeHistory(ctx context.Context, q historyQuery, targets []GitTarget, targetsKey string) (HistoryResponse, bool, error) {
	startMs := q.start.UnixMilli()
	endMs := q.endExclusive.UnixMilli()

	dbDays := make(map[string]*HistoryDay)
	runnerDays := make(map[string]*HistoryDay)
	var gitDays map[string]gitHistoryDayStat
	var runnerErr error
	dbOK := true

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		dbOK = s.queryHistoryFromDBCtx(ctx, startMs, endMs, q.includeTools, mapDayGetter(dbDays))
	}()
	go func() {
		defer wg.Done()
		runnerErr = s.CollectRunnerHistory(startMs, endMs, mapDayGetter(runnerDays))
	}()
	go func() {
		defer wg.Done()
		gitDays = s.collectGitHistoryCached(ctx, targets, targetsKey, q.start, q.endExclusive, q.todayStart, q.forced)
	}()
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return HistoryResponse{}, false, err
	}

	if runnerErr != nil {
		return HistoryResponse{}, false, runnerErr
	}

	byDate := make(map[string]*HistoryDay, q.days)
	mergeHistoryMaps(byDate, dbDays)
	mergeHistoryMaps(byDate, runnerDays)
	day := mapDayGetter(byDate)
	for key, gs := range gitDays {
		target := day(key)
		target.Commits += gs.Commits
		target.Additions += gs.Additions
		target.Deletions += gs.Deletions
		target.Files += gs.Files
	}

	sortHistoryModels(byDate)
	res := HistoryResponse{
		StartDate: q.start.Format("2006-01-02"),
		EndDate:   q.end.Format("2006-01-02"),
		Timezone:  q.start.Location().String(),
		Days:      make([]HistoryDay, 0, q.days),
	}
	for i := 0; i < q.days; i++ {
		key := q.start.AddDate(0, 0, i).Format("2006-01-02")
		res.Days = append(res.Days, *day(key))
	}
	return res, dbOK, nil
}

func mapDayGetter(byDate map[string]*HistoryDay) func(string) *HistoryDay {
	return func(key string) *HistoryDay {
		if d, ok := byDate[key]; ok {
			return d
		}
		d := &HistoryDay{Date: key, Models: []HistoryModelStat{}}
		byDate[key] = d
		return d
	}
}
