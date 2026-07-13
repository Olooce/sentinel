package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

func loadINI(path string) (*Config, error) {
	f, err := ini.Load(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	for _, section := range f.Sections() {
		name := section.Name()
		if !strings.HasPrefix(name, "service.") && !strings.HasPrefix(name, "service:") {
			continue // skip DEFAULT and unrelated sections
		}

		svc := Service{}
		svc.ID = section.Key("service_id").MustInt(0)
		svc.Name = section.Key("service_name").MustString("")
		svc.Host = section.Key("service_host").MustString("")
		svc.Port = section.Key("service_port").MustInt(0)
		svc.ResourceURI = section.Key("service_resource_uri").MustString("/")
		svc.Method = section.Key("service_method").MustString("GET")
		svc.MonitoringInterval = section.Key("monitoring_interval").MustInt(0)
		svc.MonitoringIntervalUnit = section.Key("monitoring_interval_unit").MustString("Minutes")
		svc.FileLoggingInterval = section.Key("file_logging_interval").MustString("Daily")
		svc.LogArchivingInterval = section.Key("log_archiving_interval").MustString("Weekly")
		svc.ArchiveFormat = section.Key("archive_format").MustString(".gzip")

		if section.HasKey("enable_file_logging") {
			val := section.Key("enable_file_logging").MustBool(true)
			svc.EnableFileLogging = &val
		}
		if section.HasKey("enable_logs_archiving") {
			val := section.Key("enable_logs_archiving").MustBool(false)
			svc.EnableLogsArchiving = &val
		}

		if svc.ID == 0 {
			// fall back to parsing the ID out of the section name, e.g. service.1
			parts := strings.SplitN(name, ".", 2)
			if len(parts) == 1 {
				parts = strings.SplitN(name, ":", 2)
			}
			if len(parts) == 2 {
				if id, convErr := strconv.Atoi(parts[1]); convErr == nil {
					svc.ID = id
				}
			}
		}

		cfg.Services = append(cfg.Services, svc)
	}

	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("no [service.<id>] sections found")
	}

	return cfg, nil
}
