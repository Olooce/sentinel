package events

import "time"

type Event struct {
	ServiceID         int           `json:"service_id"`
	ServiceName       string        `json:"service_name"`
	ApplicationStatus string        `json:"application_status"`
	ServerStatus      string        `json:"server_status"`
	CheckedAt         time.Time     `json:"checked_at"`
	ResponseTime      time.Duration `json:"response_time"`
	Error             string        `json:"error,omitempty"`
}
