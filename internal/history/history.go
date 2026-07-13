package history

import (
	"database/sql"
	"fmt"
	"time"
)

type Record struct {
	ServiceID         int           `json:"service_id"`
	ServiceName       string        `json:"service_name"`
	ApplicationStatus string        `json:"application_status"`
	ServerStatus      string        `json:"server_status"`
	CheckedAt         time.Time     `json:"checked_at"`
	ResponseTime      time.Duration `json:"response_time"`
	Error             string        `json:"error,omitempty"`
}

type Store struct {
	db       *sql.DB
	capacity int
}

const schemaHistory = `
CREATE TABLE IF NOT EXISTS history_records (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	service_id          INTEGER NOT NULL,
	service_name        TEXT NOT NULL,
	application_status  TEXT NOT NULL DEFAULT '',
	server_status       TEXT NOT NULL DEFAULT '',
	checked_at          DATETIME NOT NULL,
	response_time_ns    INTEGER NOT NULL DEFAULT 0,
	error               TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_history_service_checked
	ON history_records (service_id, id);`

func NewStore(db *sql.DB, capacity int) (*Store, error) {
	if capacity <= 0 {
		capacity = 200
	}
	if _, err := db.Exec(schemaHistory); err != nil {
		return nil, fmt.Errorf("create history_records table: %w", err)
	}
	return &Store{db: db, capacity: capacity}, nil
}

func (s *Store) Add(r Record) {
	_, err := s.db.Exec(`
		INSERT INTO history_records (
			service_id, service_name, application_status, server_status,
			checked_at, response_time_ns, error
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ServiceID, r.ServiceName, r.ApplicationStatus, r.ServerStatus,
		r.CheckedAt, int64(r.ResponseTime), r.Error,
	)
	if err != nil {
		return
	}

	// Keep only the most recent `capacity` rows for this service.
	_, _ = s.db.Exec(`
		DELETE FROM history_records
		WHERE service_id = ? AND id NOT IN (
			SELECT id FROM history_records
			WHERE service_id = ?
			ORDER BY id DESC
			LIMIT ?
		)`, r.ServiceID, r.ServiceID, s.capacity)
}

func (s *Store) Get(serviceID int, limit int) []Record {
	query := `
		SELECT service_id, service_name, application_status, server_status,
		       checked_at, response_time_ns, error
		FROM history_records
		WHERE service_id = ?
		ORDER BY id ASC`
	args := []any{serviceID}

	if limit > 0 {
		query = `
			SELECT service_id, service_name, application_status, server_status,
			       checked_at, response_time_ns, error
			FROM (
				SELECT * FROM history_records
				WHERE service_id = ?
				ORDER BY id DESC
				LIMIT ?
			) sub ORDER BY id ASC`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]Record, 0)
	for rows.Next() {
		var (
			rec   Record
			respN int64
		)
		if err := rows.Scan(
			&rec.ServiceID, &rec.ServiceName, &rec.ApplicationStatus, &rec.ServerStatus,
			&rec.CheckedAt, &respN, &rec.Error,
		); err != nil {
			continue
		}
		rec.ResponseTime = time.Duration(respN)
		out = append(out, rec)
	}
	return out
}
