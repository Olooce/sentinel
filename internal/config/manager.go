package config

import (
	"fmt"
	"sync"
)

type Manager struct {
	mu    sync.RWMutex
	cfg   *Config
	path  string
	Sched Scheduler
}

type Scheduler interface {
	StopService(id int)
	StartService(svc Service) error
	UpdateService(svc Service) error
}

func NewManager(path string, sched Scheduler) (*Manager, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, path: path, Sched: sched}, nil
}

func (m *Manager) GetServices() []Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]Service, len(m.cfg.Services))
	copy(cp, m.cfg.Services)
	return cp
}

func (m *Manager) GetService(id int) (Service, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.FindByID(id)
}

func (m *Manager) UpdateServices(services []Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range services {
		applyDefaults(&services[i])
		if err := Validate(services[i]); err != nil {
			return err
		}
	}
	m.cfg.Services = services
	return m.cfg.Save(m.path)
}

func (m *Manager) Reload() error {
	newCfg, err := Load(m.path)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	m.mu.Lock()
	oldServices := make(map[int]Service, len(m.cfg.Services))
	for _, s := range m.cfg.Services {
		oldServices[s.ID] = s
	}
	newServices := make(map[int]Service, len(newCfg.Services))
	for _, s := range newCfg.Services {
		newServices[s.ID] = s
	}
	m.cfg = newCfg
	m.mu.Unlock()

	// stop removed services
	for id := range oldServices {
		if _, exists := newServices[id]; !exists {
			m.Sched.StopService(id)
		}
	}
	// start new or restart changed services
	for id, newSvc := range newServices {
		oldSvc, exists := oldServices[id]
		if !exists {
			if err := m.Sched.StartService(newSvc); err != nil {
				return fmt.Errorf("start service %d: %w", id, err)
			}
		} else if serviceChanged(oldSvc, newSvc) {
			if err := m.Sched.UpdateService(newSvc); err != nil {
				return fmt.Errorf("update service %d: %w", id, err)
			}
		}
	}
	return nil
}

func serviceChanged(a, b Service) bool {
	return a.Host != b.Host || a.Port != b.Port || a.ResourceURI != b.ResourceURI ||
		a.Method != b.Method || a.MonitoringInterval != b.MonitoringInterval ||
		a.MonitoringIntervalUnit != b.MonitoringIntervalUnit
}
