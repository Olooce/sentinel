package store

import (
	"database/sql"
	"fmt"
	"time"

	"sentinel/internal/monitor"
)

type ServiceStatus struct {
	ServiceID             int       `json:"service_id"`
	ServiceName           string    `json:"service_name"`
	ApplicationStatus     string    `json:"application_status"`
	ApplicationStatusDate time.Time `json:"application_status_date"`
	ServerStatus          string    `json:"server_status"`
	ServerStatusDate      time.Time `json:"server_status_date"`
}

type Store struct {
	db *sql.DB
}

const schemaServiceStatus = `
CREATE TABLE IF NOT EXISTS service_status (
	service_id               INTEGER PRIMARY KEY,
	service_name             TEXT NOT NULL,
	application_status       TEXT NOT NULL DEFAULT '',
	application_status_date  DATETIME,
	server_status            TEXT NOT NULL DEFAULT '',
	server_status_date       DATETIME
);`

func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schemaServiceStatus); err != nil {
		return nil, fmt.Errorf("create service_status table: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Update(id int, name string, app, srv *monitor.Status) error {
	var (
		appStatus string
		appDate   sql.NullTime
		srvStatus string
		srvDate   sql.NullTime
	)

	if existing, ok, err := s.get(id); err != nil {
		return err
	} else if ok {
		appStatus, srvStatus = existing.ApplicationStatus, existing.ServerStatus
		if !existing.ApplicationStatusDate.IsZero() {
			appDate = sql.NullTime{Time: existing.ApplicationStatusDate, Valid: true}
		}
		if !existing.ServerStatusDate.IsZero() {
			srvDate = sql.NullTime{Time: existing.ServerStatusDate, Valid: true}
		}
	}

	if app != nil {
		appStatus = app.String()
		appDate = sql.NullTime{Time: app.CheckedAt, Valid: true}
	}
	if srv != nil {
		srvStatus = srv.String()
		srvDate = sql.NullTime{Time: srv.CheckedAt, Valid: true}
	}

	_, err := s.db.Exec(`
		INSERT INTO service_status (
			service_id, service_name, application_status, application_status_date,
			server_status, server_status_date
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(service_id) DO UPDATE SET
			service_name             = excluded.service_name,
			application_status       = excluded.application_status,
			application_status_date  = excluded.application_status_date,
			server_status             = excluded.server_status,
			server_status_date        = excluded.server_status_date
	`, id, name, appStatus, appDate, srvStatus, srvDate)
	if err != nil {
		return fmt.Errorf("update service_status: %w", err)
	}
	return nil
}

func (s *Store) Get(id int) (*ServiceStatus, bool) {
	rec, ok, err := s.get(id)
	if err != nil {
		return nil, false
	}
	return rec, ok
}

func (s *Store) get(id int) (*ServiceStatus, bool, error) {
	row := s.db.QueryRow(`
		SELECT service_id, service_name, application_status, application_status_date,
		       server_status, server_status_date
		FROM service_status WHERE service_id = ?`, id)

	rec, err := scanServiceStatus(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get service_status: %w", err)
	}
	return rec, true, nil
}

func (s *Store) List() []*ServiceStatus {
	rows, err := s.db.Query(`
		SELECT service_id, service_name, application_status, application_status_date,
		       server_status, server_status_date
		FROM service_status ORDER BY service_id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]*ServiceStatus, 0)
	for rows.Next() {
		rec, err := scanServiceStatus(rows)
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out
}

type scanner interface {
	Scan(dest ...any) error
}

func scanServiceStatus(sc scanner) (*ServiceStatus, error) {
	var (
		rec     ServiceStatus
		appDate sql.NullTime
		srvDate sql.NullTime
	)
	if err := sc.Scan(
		&rec.ServiceID, &rec.ServiceName, &rec.ApplicationStatus, &appDate,
		&rec.ServerStatus, &srvDate,
	); err != nil {
		return nil, err
	}
	if appDate.Valid {
		rec.ApplicationStatusDate = appDate.Time
	}
	if srvDate.Valid {
		rec.ServerStatusDate = srvDate.Time
	}
	return &rec, nil
}
