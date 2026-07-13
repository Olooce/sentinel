package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"sentinel/internal/config"
	"sentinel/internal/events"
	"sentinel/internal/history"
	"sentinel/internal/logger"
	"sentinel/internal/monitor"
	"sentinel/internal/store"
)

type Scheduler struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	runners map[int]context.CancelFunc

	checker *monitor.Checker
	lg      *logger.Logger
	st      *store.Store
	broker  *events.Broker
	history *history.Store
}

func New(checker *monitor.Checker, lg *logger.Logger, st *store.Store) *Scheduler {
	return &Scheduler{
		runners: make(map[int]context.CancelFunc),
		checker: checker,
		lg:      lg,
		st:      st,
	}
}

func (s *Scheduler) SetBroker(b *events.Broker)  { s.broker = b }
func (s *Scheduler) SetHistory(h *history.Store) { s.history = h }

func (s *Scheduler) StartService(svc config.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.runners[svc.ID]; exists {
		return nil // already running
	}

	interval, err := monitor.ParseInterval(svc.MonitoringInterval, svc.MonitoringIntervalUnit)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.runners[svc.ID] = cancel
	s.wg.Add(1)
	go s.monitorService(ctx, svc, interval)
	return nil
}

func (s *Scheduler) StopService(id int) {
	s.mu.Lock()
	cancel, exists := s.runners[id]
	if exists {
		delete(s.runners, id)
		cancel()
	}
	s.mu.Unlock()
}

func (s *Scheduler) UpdateService(svc config.Service) error {
	s.StopService(svc.ID)
	return s.StartService(svc)
}

func (s *Scheduler) StopAll() {
	s.mu.Lock()
	for _, cancel := range s.runners {
		cancel()
	}
	s.runners = make(map[int]context.CancelFunc)
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Scheduler) CheckNow(svc config.Service) {
	app := s.checker.CheckApplication(svc)
	srv := s.checker.CheckServer(svc)

	log.Printf("check service %d (%s): app=%s srv=%s", svc.ID, svc.Name, app.String(), srv.String())

	if err := s.st.Update(svc.ID, svc.Name, &app, &srv); err != nil {
		log.Printf("store update failed: %v", err)
	}
	if svc.EnableFileLogging != nil && *svc.EnableFileLogging {
		s.lg.LogApplication(svc, app)
		s.lg.LogServer(svc, srv)
	}

	if s.broker != nil {
		ev := events.Event{
			ServiceID:         svc.ID,
			ServiceName:       svc.Name,
			ApplicationStatus: app.String(),
			ServerStatus:      srv.String(),
			CheckedAt:         app.CheckedAt,
			ResponseTime:      app.ResponseTime,
			Error:             orError(app.Error, srv.Error),
		}
		s.broker.Publish(ev)
	}
	if s.history != nil {
		s.history.Add(history.Record{
			ServiceID:         svc.ID,
			ServiceName:       svc.Name,
			ApplicationStatus: app.String(),
			ServerStatus:      srv.String(),
			CheckedAt:         app.CheckedAt,
			ResponseTime:      app.ResponseTime,
			Error:             orError(app.Error, srv.Error),
		})
	}
}

func (s *Scheduler) monitorService(ctx context.Context, svc config.Service, interval time.Duration) {
	defer s.wg.Done()

	// immediate check
	s.CheckNow(svc)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.CheckNow(svc)
		case <-ctx.Done():
			return
		}
	}
}

func orError(appErr, srvErr string) string {
	if appErr != "" {
		return appErr
	}
	return srvErr
}
