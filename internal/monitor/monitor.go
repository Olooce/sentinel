package monitor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"sentinel/internal/config"
)

type Status struct {
	Up           bool          `json:"up"`
	CheckedAt    time.Time     `json:"checked_at"`
	Error        string        `json:"error,omitempty"`
	ResponseTime time.Duration `json:"response_time"`
}

func (s Status) String() string {
	if s.Up {
		return "UP"
	}
	return "DOWN"
}

type Checker struct {
	httpClient  *http.Client
	dialTimeout time.Duration
}

func NewChecker() *Checker {
	return &Checker{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		dialTimeout: 5 * time.Second,
	}
}

func (c *Checker) CheckApplication(svc config.Service) Status {
	start := time.Now()
	scheme := "http"
	if svc.Port == 443 {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:%d%s", scheme, svc.Host, svc.Port, svc.ResourceURI)

	ctx, cancel := context.WithTimeout(context.Background(), c.httpClient.Timeout)
	defer cancel()

	method := svc.Method
	if method == "" {
		method = "GET"
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		elapsed := time.Since(start)
		return Status{Up: false, CheckedAt: start, Error: err.Error(), ResponseTime: elapsed}
	}

	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return Status{Up: false, CheckedAt: start, Error: err.Error(), ResponseTime: elapsed}
	}
	defer resp.Body.Close()

	return Status{Up: resp.StatusCode < 400, CheckedAt: start, ResponseTime: elapsed}
}

func (c *Checker) CheckServer(svc config.Service) Status {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", svc.Host, svc.Port)

	conn, err := net.DialTimeout("tcp", addr, c.dialTimeout)
	elapsed := time.Since(start)
	if err != nil {
		return Status{Up: false, CheckedAt: start, Error: err.Error(), ResponseTime: elapsed}
	}
	conn.Close()

	return Status{Up: true, CheckedAt: start, ResponseTime: elapsed}
}

func ParseInterval(value int, unit string) (time.Duration, error) {
	if value <= 0 {
		return 0, fmt.Errorf("monitoring interval must be greater than zero")
	}
	d := time.Duration(value)
	switch unit {
	case "Seconds":
		return d * time.Second, nil
	case "Minutes":
		return d * time.Minute, nil
	case "Hours":
		return d * time.Hour, nil
	case "Days":
		return d * 24 * time.Hour, nil
	case "Weeks":
		return d * 7 * 24 * time.Hour, nil
	case "Months":
		return d * 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown monitoring_interval_unit: %s", unit)
	}
}
