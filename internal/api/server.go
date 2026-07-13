package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sentinel/internal/config"
	"sentinel/internal/events"
	"sentinel/internal/history"
	"sentinel/internal/logger"
	"sentinel/internal/monitor"
	"sentinel/internal/runner"
	"sentinel/internal/scheduler"
	"sentinel/internal/store"
)

type Server struct {
	store   *store.Store
	broker  *events.Broker
	sched   *scheduler.Scheduler
	mgr     *config.Manager
	history *history.Store
	lg      *logger.Logger
	logDir  string
	runner  *runner.Runner
}

func NewServer(
	st *store.Store,
	broker *events.Broker,
	sched *scheduler.Scheduler,
	mgr *config.Manager,
	hist *history.Store,
	lg *logger.Logger,
	logDir string,
	r *runner.Runner,
) *Server {
	return &Server{
		store:   st,
		broker:  broker,
		sched:   sched,
		mgr:     mgr,
		history: hist,
		lg:      lg,
		logDir:  logDir,
		runner:  r,
	}
}

func (s *Server) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.GET("/status", s.handleStatus)
		api.GET("/services", s.handleServices)
		api.GET("/services/:id", s.handleServiceDetail)
		api.GET("/history/:id", s.handleHistory)
		api.GET("/logs/:serviceId", s.handleLogs)
		api.GET("/config", s.handleGetConfig)
		api.PUT("/config", s.handlePutConfig)
		api.POST("/config/reload", s.handleReload)
		api.POST("/reload", s.handleReload) // backward-compat alias
		api.POST("/check/:id", s.handleManualCheck)
		api.POST("/start", s.handleStart)
		api.POST("/stop", s.handleStop)
		api.GET("/events", s.handleSSE)
	}
}

func (s *Server) handleStatus(c *gin.Context) {
	list := s.store.List()
	total := len(list)
	upApp, upSrv := 0, 0
	for _, ss := range list {
		if ss.ApplicationStatus == "UP" {
			upApp++
		}
		if ss.ServerStatus == "UP" {
			upSrv++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"total_services":   total,
		"application_up":   upApp,
		"application_down": total - upApp,
		"server_up":        upSrv,
		"server_down":      total - upSrv,
	})
}

type ServiceInfo struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	ApplicationStatus  string `json:"application_status"`
	ServerStatus       string `json:"server_status"`
	LastChecked        string `json:"last_checked"`
	NextCheck          string `json:"next_check"`
	MonitoringInterval string `json:"monitoring_interval"`
	ResponseTimeMs     int64  `json:"response_time_ms"`
}

func (s *Server) handleServices(c *gin.Context) {
	services := s.mgr.GetServices()
	out := make([]ServiceInfo, 0, len(services))
	for _, svc := range services {
		status, _ := s.store.Get(svc.ID)
		appStatus, srvStatus := "UNKNOWN", "UNKNOWN"
		var lastChecked time.Time
		var responseTimeMs int64
		if status != nil {
			appStatus = status.ApplicationStatus
			srvStatus = status.ServerStatus
			if !status.ApplicationStatusDate.IsZero() {
				lastChecked = status.ApplicationStatusDate
			}
		}
		// pull latest response time from history
		if s.history != nil {
			recs := s.history.Get(svc.ID, 1)
			if len(recs) > 0 {
				responseTimeMs = recs[len(recs)-1].ResponseTime.Milliseconds()
			}
		}
		interval, _ := monitor.ParseInterval(svc.MonitoringInterval, svc.MonitoringIntervalUnit)
		nextCheck := ""
		if !lastChecked.IsZero() {
			nextCheck = lastChecked.Add(interval).Format(time.RFC3339)
		}
		out = append(out, ServiceInfo{
			ID:                 svc.ID,
			Name:               svc.Name,
			Host:               svc.Host,
			Port:               svc.Port,
			ApplicationStatus:  appStatus,
			ServerStatus:       srvStatus,
			LastChecked:        lastChecked.Format(time.RFC3339),
			NextCheck:          nextCheck,
			MonitoringInterval: fmt.Sprintf("%d %s", svc.MonitoringInterval, svc.MonitoringIntervalUnit),
			ResponseTimeMs:     responseTimeMs,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleServiceDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	svc, ok := s.mgr.GetService(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	st, _ := s.store.Get(id)
	hist := s.history.Get(id, 50)
	c.JSON(http.StatusOK, gin.H{
		"service": svc,
		"status":  st,
		"history": hist,
	})
}

func (s *Server) handleHistory(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	hist := s.history.Get(id, 100)
	c.JSON(http.StatusOK, hist)
}

func (s *Server) handleLogs(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("serviceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid serviceId"})
		return
	}
	svcDir := filepath.Join(s.logDir, fmt.Sprintf("service_%d", id))
	entries, readErr := os.ReadDir(svcDir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			c.JSON(http.StatusOK, gin.H{"lines": []string{}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": readErr.Error()})
		return
	}

	// Collect the two most-recent .log files (application + server)
	type logFile struct {
		name    string
		modTime time.Time
	}
	var logFiles []logFile
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			info, infoErr := e.Info()
			if infoErr == nil {
				logFiles = append(logFiles, logFile{name: e.Name(), modTime: info.ModTime()})
			}
		}
	}
	// sort newest-first
	for i := 0; i < len(logFiles)-1; i++ {
		for j := i + 1; j < len(logFiles); j++ {
			if logFiles[j].modTime.After(logFiles[i].modTime) {
				logFiles[i], logFiles[j] = logFiles[j], logFiles[i]
			}
		}
	}
	// read up to 2 files, last 100 lines total
	var lines []string
	limit := 2
	if len(logFiles) < limit {
		limit = len(logFiles)
	}
	for _, lf := range logFiles[:limit] {
		data, readFileErr := fs.ReadFile(os.DirFS(svcDir), lf.name)
		if readFileErr != nil {
			continue
		}
		fileLines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		// take last 50 lines per file
		if len(fileLines) > 50 {
			fileLines = fileLines[len(fileLines)-50:]
		}
		lines = append(fileLines, lines...) // prepend older
	}
	c.JSON(http.StatusOK, gin.H{"service_id": id, "lines": lines})
}

func (s *Server) handleGetConfig(c *gin.Context) {
	c.JSON(http.StatusOK, s.mgr.GetServices())
}

func (s *Server) handlePutConfig(c *gin.Context) {
	var services []config.Service
	if err := c.ShouldBindJSON(&services); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.mgr.UpdateServices(services); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

func (s *Server) handleReload(c *gin.Context) {
	if err := s.mgr.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "reloaded"})
}

func (s *Server) handleStart(c *gin.Context) {
	if s.runner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "runner not available"})
		return
	}
	s.runner.Start()
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

func (s *Server) handleStop(c *gin.Context) {
	if s.runner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "runner not available"})
		return
	}
	s.runner.Stop()
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

func (s *Server) handleManualCheck(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	svc, ok := s.mgr.GetService(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	app := monitor.NewChecker().CheckApplication(svc)
	srv := monitor.NewChecker().CheckServer(svc)

	s.store.Update(svc.ID, svc.Name, &app, &srv)
	if svc.EnableFileLogging != nil && *svc.EnableFileLogging {
		s.lg.LogApplication(svc, app)
		s.lg.LogServer(svc, srv)
	}
	if s.broker != nil {
		s.broker.Publish(events.Event{
			ServiceID:         svc.ID,
			ServiceName:       svc.Name,
			ApplicationStatus: app.String(),
			ServerStatus:      srv.String(),
			CheckedAt:         app.CheckedAt,
			ResponseTime:      app.ResponseTime,
			Error:             orError(app.Error, srv.Error),
		})
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
	c.JSON(http.StatusOK, gin.H{
		"service_id":         svc.ID,
		"application_status": app.String(),
		"server_status":      srv.String(),
		"response_time_ms":   app.ResponseTime.Milliseconds(),
		"checked_at":         app.CheckedAt.Format(time.RFC3339),
	})
}

func (s *Server) handleSSE(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering if proxied

	ch := s.broker.Subscribe()
	defer s.broker.Unsubscribe(ch)

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
		case <-c.Request.Context().Done():
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
