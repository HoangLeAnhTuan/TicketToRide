package persistence

import (
	"context"
	"testing"
	"time"
)

func TestBoundedQueueAndShutdown(t *testing.T) {
	p := New(1, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan Result, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !p.Submit(Job{Context: ctx, Done: done, Run: func(context.Context) (any, error) { close(started); <-release; return "first", nil }}) {
		t.Fatal("initial job rejected")
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not start")
	}
	if !p.Submit(Job{Context: ctx, Done: done, Run: func(context.Context) (any, error) { return "second", nil }}) {
		t.Fatal("bounded queue not usable")
	}
	if p.Submit(Job{Context: ctx, Done: done, Run: func(context.Context) (any, error) { return nil, nil }}) {
		t.Fatal("unbounded job admission")
	}
	close(release)
	for range 2 {
		select {
		case result := <-done:
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		case <-ctx.Done():
			t.Fatal("accepted job lost")
		}
	}
	p.Close()
	if p.Submit(Job{Context: ctx, Done: done}) {
		t.Fatal("closed pool accepted work")
	}
}
