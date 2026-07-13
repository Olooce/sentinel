package logger

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sentinel/internal/config"
	"sentinel/internal/monitor"
)

type Logger struct {
	mu          sync.Mutex
	baseDir     string
	lastArchive map[string]time.Time // key: "<serviceID>-<kind>"
}

func NewLogger(baseDir string) (*Logger, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	return &Logger{baseDir: baseDir, lastArchive: map[string]time.Time{}}, nil
}

func (l *Logger) LogApplication(svc config.Service, status monitor.Status) error {
	line := fmt.Sprintf("%s | service_id=%d service_name=%q application=%s error=%q",
		status.CheckedAt.Format(time.RFC3339), svc.ID, svc.Name, status.String(), status.Error)
	return l.write(svc, "application", line)
}

func (l *Logger) LogServer(svc config.Service, status monitor.Status) error {
	line := fmt.Sprintf("%s | service_id=%d service_name=%q server=%s error=%q",
		status.CheckedAt.Format(time.RFC3339), svc.ID, svc.Name, status.String(), status.Error)
	return l.write(svc, "server", line)
}

func prefixFor(interval string, t time.Time) string {
	switch interval {
	case "Hourly":
		return t.Format("2006010215")
	case "Daily":
		return t.Format("20060102")
	case "Weekly":
		year, week := t.ISOWeek()
		return fmt.Sprintf("%d%02dW", year, week)
	case "Monthly":
		return t.Format("200601")
	default:
		return t.Format("20060102")
	}
}

func archiveIntervalDuration(interval string) time.Duration {
	switch interval {
	case "Hourly":
		return time.Hour
	case "Daily":
		return 24 * time.Hour
	case "Weekly":
		return 7 * 24 * time.Hour
	case "Monthly":
		return 30 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

func (l *Logger) serviceDir(svc config.Service) string {
	return filepath.Join(l.baseDir, fmt.Sprintf("service_%d", svc.ID))
}

func (l *Logger) write(svc config.Service, kind, line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	svcDir := l.serviceDir(svc)
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		return err
	}

	prefix := prefixFor(svc.FileLoggingInterval, time.Now())
	filename := fmt.Sprintf("%s_%s.log", prefix, kind)
	fullPath := filepath.Join(svcDir, filename)

	f, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}

	if svc.EnableLogsArchiving != nil && *svc.EnableLogsArchiving {
		l.archiveOldFilesLocked(svc, svcDir, kind, prefix)
	}
	return nil
}

func (l *Logger) archiveOldFilesLocked(svc config.Service, svcDir, kind, currentPrefix string) {
	key := fmt.Sprintf("%d-%s", svc.ID, kind)
	interval := archiveIntervalDuration(svc.LogArchivingInterval)

	if last, ok := l.lastArchive[key]; ok && time.Since(last) < interval {
		return
	}

	entries, err := os.ReadDir(svcDir)
	if err != nil {
		return
	}

	suffix := "_" + kind + ".log"
	activeName := currentPrefix + suffix

	var toArchive []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) || e.Name() == activeName {
			continue
		}
		toArchive = append(toArchive, filepath.Join(svcDir, e.Name()))
	}

	l.lastArchive[key] = time.Now()
	if len(toArchive) == 0 {
		return
	}

	archiveDir := filepath.Join(svcDir, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return
	}

	format := svc.ArchiveFormat
	if format == "" {
		format = ".gzip"
	}

	var archErr error
	if format == ".zip" {
		dest := filepath.Join(archiveDir, fmt.Sprintf("%s_%s_%d.zip", kind, time.Now().Format("20060102150405"), svc.ID))
		archErr = zipFiles(toArchive, dest)
	} else {
		archErr = gzipFiles(toArchive, archiveDir)
	}

	if archErr == nil {
		for _, f := range toArchive {
			os.Remove(f)
		}
	}
}

func zipFiles(files []string, dest string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	for _, path := range files {
		if err := addFileToZip(zw, path); err != nil {
			return err
		}
	}
	return nil
}

func addFileToZip(zw *zip.Writer, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	w, err := zw.Create(filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

func gzipFiles(files []string, archiveDir string) error {
	for _, path := range files {
		if err := gzipFile(path, archiveDir); err != nil {
			return err
		}
	}
	return nil
}

func gzipFile(path, archiveDir string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	dest := filepath.Join(archiveDir, filepath.Base(path)+".gz")
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	gw.Name = filepath.Base(path)
	defer gw.Close()

	_, err = io.Copy(gw, src)
	return err
}
