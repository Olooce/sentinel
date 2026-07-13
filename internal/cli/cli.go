package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gin-gonic/gin"

	"sentinel/internal/api"
	"sentinel/internal/config"
	"sentinel/internal/dashboard"
	"sentinel/internal/events"
	"sentinel/internal/history"
	"sentinel/internal/logger"
	"sentinel/internal/monitor"
	"sentinel/internal/runner"
	"sentinel/internal/store"
)

const (
	defaultConfigPath      = "services/services.yaml"
	defaultLogDir          = "logs"
	defaultDBPath          = "sentinel.db"
	defaultHistoryCapacity = 200
	defaultPIDFile         = "sentinel.pid"
	defaultHTTPAddr        = ":8080"
)

func Execute() {
	args, configPath := extractConfigFlag(os.Args[1:], defaultConfigPath)

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "application":
		err = cmdCheckStatus(configPath, args, "application")
	case "server":
		err = cmdCheckStatus(configPath, args, "server")
	case "service":
		if len(args) >= 2 && args[1] == "list" {
			err = cmdServiceList(configPath)
		} else {
			printUsage()
			os.Exit(1)
		}
	case "start":
		err = cmdStart(configPath)
	case "stop":
		err = cmdStop()
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func extractConfigFlag(args []string, fallback string) ([]string, string) {
	out := make([]string, 0, len(args))
	path := fallback

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-c":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--config="):
			path = strings.TrimPrefix(a, "--config=")
		default:
			out = append(out, a)
		}
	}
	return out, path
}

func printUsage() {
	fmt.Println(`sentinel - Service & server availability monitor

Usage:
  sentinel application status <ServiceID>   Check a service's application status
  sentinel server status <ServiceID>        Check the status of the server hosting a service
  sentinel service list                     List all services and their last known status
  sentinel start                            Start the monitoring daemon
  sentinel stop                             Stop the monitoring daemon

Flags:
  --config, -c <path>   Path to the config file (.json, .yaml, .xml, .ini). Default: config.yaml`)
}

func cmdCheckStatus(configPath string, args []string, kind string) error {
	if len(args) < 3 || args[1] != "status" {
		printUsage()
		os.Exit(1)
	}
	id, err := strconv.Atoi(args[2])
	if err != nil {
		return fmt.Errorf("invalid service ID %q", args[2])
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	svc, ok := cfg.FindByID(id)
	if !ok {
		return fmt.Errorf("no service with ID %d in %s", id, configPath)
	}

	checker := monitor.NewChecker()
	var status monitor.Status
	if kind == "application" {
		status = checker.CheckApplication(svc)
	} else {
		status = checker.CheckServer(svc)
	}

	if db, err := store.Open(defaultDBPath); err == nil {
		defer db.Close()
		if st, err := store.NewStore(db); err == nil {
			app, srv := (*monitor.Status)(nil), (*monitor.Status)(nil)
			if kind == "application" {
				app = &status
			} else {
				srv = &status
			}
			_ = st.Update(svc.ID, svc.Name, app, srv)
		}
	}

	fmt.Printf("Service %d (%s) - %s: %s\n", svc.ID, svc.Name, strings.Title(kind), status.String())
	if status.Error != "" {
		fmt.Printf("  reason: %s\n", status.Error)
	}
	fmt.Printf("  checked at: %s\n", status.CheckedAt.Format(time.RFC3339))
	return nil
}

func cmdServiceList(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	db, err := store.Open(defaultDBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	st, err := store.NewStore(db)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE ID\tSERVICE NAME\tAPP STATUS\tAPP STATUS DATE\tSERVER STATUS\tSERVER STATUS DATE")

	for _, svc := range cfg.Services {
		if s, ok := st.Get(svc.ID); ok {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				svc.ID, svc.Name,
				orUnknown(s.ApplicationStatus), formatTime(s.ApplicationStatusDate),
				orUnknown(s.ServerStatus), formatTime(s.ServerStatusDate))
		} else {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", svc.ID, svc.Name, "UNKNOWN", "-", "UNKNOWN", "-")
		}
	}
	return w.Flush()
}

func orUnknown(s string) string {
	if s == "" {
		return "UNKNOWN"
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func cmdStart(configPath string) error {
	if pid, alive := readAlivePID(defaultPIDFile); alive {
		return fmt.Errorf("sentinel is already running (pid %d)", pid)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	lg, err := logger.NewLogger(defaultLogDir)
	if err != nil {
		return err
	}

	db, err := store.Open(defaultDBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	st, err := store.NewStore(db)
	if err != nil {
		return err
	}
	hist, err := history.NewStore(db, defaultHistoryCapacity)
	if err != nil {
		return err
	}

	if err := writePIDFile(defaultPIDFile); err != nil {
		return err
	}
	defer os.Remove(defaultPIDFile)

	// event bus
	broker := events.NewBroker()

	// config manager
	mgr, err := config.NewManager(configPath, nil)
	if err != nil {
		return fmt.Errorf("config manager: %w", err)
	}

	// runner - owns the scheduler, publishes events
	r := runner.New(cfg, lg, st)
	r.SetEventBroker(broker)
	r.SetHistory(hist)
	mgr.Sched = r.Scheduler()

	// Gin engine
	gin.SetMode(gin.DebugMode)
	engine := gin.New()
	engine.Use(gin.Logger())

	// Register REST API routes
	apiSrv := api.NewServer(st, broker, r.Scheduler(), mgr, hist, lg, defaultLogDir, r)
	apiSrv.RegisterRoutes(engine)

	// Register dashboard / static routes
	dash := dashboard.NewHandler(st, mgr, broker)
	dash.RegisterRoutes(engine)

	httpSrv := &http.Server{Addr: defaultHTTPAddr, Handler: engine}

	go func() {
		log.Printf("Web portal listening on %s", defaultHTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server: %v", err)
		}
	}()

	r.Start()
	fmt.Printf("sentinel started (pid %d), monitoring %d service(s). Press Ctrl+C or run 'sentinel stop' to stop.\n", os.Getpid(), len(cfg.Services))

	waitForShutdownSignal()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	r.Stop()
	fmt.Println("sentinel stopped.")
	return nil
}

func cmdStop() error {
	pid, alive := readAlivePID(defaultPIDFile)
	if !alive {
		return fmt.Errorf("sentinel does not appear to be running")
	}
	if err := sendStop(pid); err != nil {
		return fmt.Errorf("failed to stop process %d: %w", pid, err)
	}
	fmt.Printf("Sent stop signal to sentinel (pid %d)\n", pid)
	return nil
}
