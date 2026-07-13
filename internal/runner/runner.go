package runner

import (
	"log"

	"sentinel/internal/config"
	"sentinel/internal/events"
	"sentinel/internal/history"
	"sentinel/internal/logger"
	"sentinel/internal/monitor"
	"sentinel/internal/scheduler"
	"sentinel/internal/store"
)

type Runner struct {
	cfg   *config.Config
	sched *scheduler.Scheduler
}

func New(cfg *config.Config, lg *logger.Logger, st *store.Store) *Runner {
	checker := monitor.NewChecker()
	s := scheduler.New(checker, lg, st)
	return &Runner{
		cfg:   cfg,
		sched: s,
	}
}

// SetEventBroker attaches an event broker to the underlying scheduler.
func (r *Runner) SetEventBroker(b *events.Broker) {
	r.sched.SetBroker(b)
}

// SetHistory attaches an in‑memory history store.
func (r *Runner) SetHistory(h *history.Store) {
	r.sched.SetHistory(h)
}

// Start launches monitoring goroutines for every configured service.
func (r *Runner) Start() {
	for _, svc := range r.cfg.Services {
		if err := r.sched.StartService(svc); err != nil {
			log.Printf("runner: start service %d: %v", svc.ID, err)
		}
	}
}

// Stop gracefully shuts down all monitoring goroutines.
func (r *Runner) Stop() {
	r.sched.StopAll()
}

// Scheduler returns the underlying scheduler (used by API to trigger manual checks).
func (r *Runner) Scheduler() *scheduler.Scheduler {
	return r.sched
}
