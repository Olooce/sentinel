package config

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Service struct {
	ID                     int    `json:"service_id" yaml:"service_id" xml:"ServiceID"`
	Name                   string `json:"service_name" yaml:"service_name" xml:"ServiceName"`
	Host                   string `json:"service_host" yaml:"service_host" xml:"ServiceHost"`
	Port                   int    `json:"service_port" yaml:"service_port" xml:"ServicePort"`
	ResourceURI            string `json:"service_resource_uri" yaml:"service_resource_uri" xml:"ServiceResourceURI"`
	Method                 string `json:"service_method" yaml:"service_method" xml:"ServiceMethod"`
	MonitoringInterval     int    `json:"monitoring_interval" yaml:"monitoring_interval" xml:"MonitoringInterval"`
	MonitoringIntervalUnit string `json:"monitoring_interval_unit" yaml:"monitoring_interval_unit" xml:"MonitoringIntervalUnit"`
	EnableFileLogging      *bool  `json:"enable_file_logging" yaml:"enable_file_logging" xml:"EnableFileLogging"`
	FileLoggingInterval    string `json:"file_logging_interval" yaml:"file_logging_interval" xml:"FileLoggingInterval"`
	EnableLogsArchiving    *bool  `json:"enable_logs_archiving" yaml:"enable_logs_archiving" xml:"EnableLogsArchiving"`
	LogArchivingInterval   string `json:"log_archiving_interval" yaml:"log_archiving_interval" xml:"LogArchivingInterval"`
	ArchiveFormat          string `json:"archive_format" yaml:"archive_format" xml:"ArchiveFormat"`
}

type Config struct {
	XMLName  xml.Name  `json:"-" yaml:"-" xml:"Config"`
	Services []Service `json:"services" yaml:"services" xml:"Services>Service"`
}

var (
	ValidIntervalUnits = map[string]bool{
		"Seconds": true, "Minutes": true, "Hours": true, "Days": true, "Weeks": true, "Months": true,
	}
	ValidLoggingIntervals = map[string]bool{
		"Hourly": true, "Daily": true, "Weekly": true, "Monthly": true,
	}
)

func applyDefaults(s *Service) {
	if s.MonitoringIntervalUnit == "" {
		s.MonitoringIntervalUnit = "Minutes"
	}
	if s.EnableFileLogging == nil {
		s.EnableFileLogging = new(bool)
		*s.EnableFileLogging = true
	}
	if s.FileLoggingInterval == "" {
		s.FileLoggingInterval = "Daily"
	}
	if s.EnableLogsArchiving == nil {
		s.EnableLogsArchiving = new(bool)
		*s.EnableLogsArchiving = false
	}
	if s.LogArchivingInterval == "" {
		s.LogArchivingInterval = "Weekly"
	}
	if s.ArchiveFormat == "" {
		s.ArchiveFormat = ".gzip"
	}
	if s.Method == "" {
		s.Method = "GET"
	}
	if s.ResourceURI == "" {
		s.ResourceURI = "/"
	}
}

func Validate(s Service) error {
	if s.ID == 0 {
		return fmt.Errorf("service is missing a service_id")
	}
	if s.Host == "" {
		return fmt.Errorf("service %d is missing a service_host", s.ID)
	}
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("service %d has an invalid service_port: %d", s.ID, s.Port)
	}
	if !ValidIntervalUnits[s.MonitoringIntervalUnit] {
		return fmt.Errorf("service %d has an invalid monitoring_interval_unit: %s", s.ID, s.MonitoringIntervalUnit)
	}
	if !ValidLoggingIntervals[s.FileLoggingInterval] {
		return fmt.Errorf("service %d has an invalid file_logging_interval: %s", s.ID, s.FileLoggingInterval)
	}
	if !ValidLoggingIntervals[s.LogArchivingInterval] {
		return fmt.Errorf("service %d has an invalid log_archiving_interval: %s", s.ID, s.LogArchivingInterval)
	}
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing JSON config: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing YAML config: %w", err)
		}
	case ".xml":
		if err := xml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing XML config: %w", err)
		}
	case ".ini":
		parsed, err := loadINI(path)
		if err != nil {
			return nil, fmt.Errorf("parsing INI config: %w", err)
		}
		cfg = *parsed
	default:
		return nil, fmt.Errorf("unsupported config file type %q (supported: .xml, .json, .yaml, .ini)", ext)
	}

	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("config file %s defines no services", path)
	}

	for i := range cfg.Services {
		applyDefaults(&cfg.Services[i])
		if err := Validate(cfg.Services[i]); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

func (c *Config) FindByID(id int) (Service, bool) {
	for _, s := range c.Services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

func (c *Config) Save(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	var data []byte
	var err error

	switch ext {
	case ".json":
		data, err = json.MarshalIndent(c, "", "  ")
	case ".yaml", ".yml":
		data, err = yaml.Marshal(c)
	case ".xml":
		data, err = xml.MarshalIndent(c, "", "  ")
	case ".ini":
		var buf bytes.Buffer
		for _, s := range c.Services {
			fmt.Fprintf(&buf, "[service.%d]\n", s.ID)
			fmt.Fprintf(&buf, "service_id = %d\n", s.ID)
			fmt.Fprintf(&buf, "service_name = %s\n", s.Name)
			fmt.Fprintf(&buf, "service_host = %s\n", s.Host)
			fmt.Fprintf(&buf, "service_port = %d\n", s.Port)
			fmt.Fprintf(&buf, "service_resource_uri = %s\n", s.ResourceURI)
			fmt.Fprintf(&buf, "service_method = %s\n", s.Method)
			fmt.Fprintf(&buf, "monitoring_interval = %d\n", s.MonitoringInterval)
			fmt.Fprintf(&buf, "monitoring_interval_unit = %s\n", s.MonitoringIntervalUnit)
			if s.EnableFileLogging != nil {
				fmt.Fprintf(&buf, "enable_file_logging = %t\n", *s.EnableFileLogging)
			}
			fmt.Fprintf(&buf, "file_logging_interval = %s\n", s.FileLoggingInterval)
			if s.EnableLogsArchiving != nil {
				fmt.Fprintf(&buf, "enable_logs_archiving = %t\n", *s.EnableLogsArchiving)
			}
			fmt.Fprintf(&buf, "log_archiving_interval = %s\n", s.LogArchivingInterval)
			fmt.Fprintf(&buf, "archive_format = %s\n", s.ArchiveFormat)
			buf.WriteByte('\n')
		}
		data = buf.Bytes()
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
