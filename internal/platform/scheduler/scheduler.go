// Package scheduler runs background jobs on fixed intervals inside the API
// process. Jobs must be safe to run on several instances at once (claim
// work with SELECT ... FOR UPDATE SKIP LOCKED) and safe to repeat.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Job is a named function run every Every.
type Job struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context) error
}

// Run starts every job and blocks until ctx is cancelled and the running
// jobs have returned. A job's run gets at most its interval (and at least a
// minute) before its context is cancelled; failures and panics are logged.
func Run(ctx context.Context, logger *slog.Logger, jobs []Job) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(j.Every)
			defer ticker.Stop()
			for {
				runOnce(ctx, logger, j)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	wg.Wait()
}

func runOnce(ctx context.Context, logger *slog.Logger, j Job) {
	defer func() {
		if p := recover(); p != nil {
			logger.ErrorContext(ctx, "job panicked", slog.String("job", j.Name), slog.Any("panic", p))
		}
	}()
	if ctx.Err() != nil {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, max(j.Every, time.Minute))
	defer cancel()
	if err := j.Run(runCtx); err != nil && ctx.Err() == nil {
		logger.ErrorContext(ctx, "job failed", slog.String("job", j.Name), slog.Any("error", err))
	}
}
