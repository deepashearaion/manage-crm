package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ticks, failures atomic.Int32
	done := make(chan struct{})
	go func() {
		Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), []Job{
			{Name: "tick", Every: 10 * time.Millisecond, Run: func(context.Context) error {
				if ticks.Add(1) == 3 {
					cancel()
				}
				return nil
			}},
			{Name: "fails", Every: 10 * time.Millisecond, Run: func(context.Context) error {
				failures.Add(1)
				if failures.Load() == 1 {
					panic("boom") // recovered and logged; the job keeps running
				}
				return errors.New("nope")
			}},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if ticks.Load() < 3 || failures.Load() < 1 {
		t.Errorf("ticks %d failures %d", ticks.Load(), failures.Load())
	}
}
