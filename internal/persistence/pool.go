// Package persistence executes bounded immutable jobs outside room event loops.
package persistence

import (
	"context"
	"sync"
)

type Result struct {
	Value any
	Err   error
}
type Job struct {
	Run     func(context.Context) (any, error)
	Done    chan<- Result
	Context context.Context
}
type Pool struct {
	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan Job
	wg     sync.WaitGroup
}

func New(workers, capacity int) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{ctx: ctx, cancel: cancel, jobs: make(chan Job, capacity)}
	for range workers {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}
func (p *Pool) Submit(j Job) bool {
	select {
	case <-p.ctx.Done():
		return false
	default:
	}
	select {
	case p.jobs <- j:
		return true
	case <-p.ctx.Done():
		return false
	default:
		return false
	}
}
func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case j := <-p.jobs:
			select {
			case <-j.Context.Done():
				continue
			default:
			}
			v, err := j.Run(j.Context)
			select {
			case j.Done <- Result{v, err}:
			case <-j.Context.Done():
			case <-p.ctx.Done():
			}
		}
	}
}
func (p *Pool) Close() { p.cancel(); p.wg.Wait() }
