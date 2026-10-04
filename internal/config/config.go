// Package config loads and validates the gateway configuration from a YAML file.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
	"uuid"

	"github.com/vitalvas/gokit/xconfig"
)

// Config is the top-level gateway configuration.
type Config struct {
	Listen          string                `yaml:"listen" default:":8080"`
	VictoriaMetrics VictoriaMetricsConfig `yaml:"victoriametrics"`
	RateLimit       RateLimitConfig       `yaml:"ratelimit"`
	Checks          map[string]Check      `yaml:"checks"`
}

// VictoriaMetricsConfig describes the target vmsingle instance.
type VictoriaMetricsConfig struct {
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout" default:"5s"`
}

// RateLimitConfig configures the request rate limits, in requests per minute.
type RateLimitConfig struct {
	CheckRPM int `yaml:"check_rpm" default:"10"`
	IPRPM    int `yaml:"ip_rpm" default:"50"`
}

// Check describes a single registered healthcheck and its static labels.
type Check struct {
	Labels map[string]string `yaml:"labels"`
}

// Load reads the configuration from the given YAML file path, applies defaults,
// and validates it.
func Load(path string) (*Config, error) {
	var cfg Config

	if err := xconfig.Load(&cfg, xconfig.WithFiles(path)); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		return errors.New("config: listen must not be empty")
	}

	if c.VictoriaMetrics.URL == "" {
		return errors.New("config: victoriametrics.url must not be empty")
	}

	parsed, err := url.Parse(c.VictoriaMetrics.URL)
	if err != nil {
		return fmt.Errorf("config: victoriametrics.url is invalid: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("config: victoriametrics.url must be http or https, got %q", parsed.Scheme)
	}

	if c.VictoriaMetrics.Timeout <= 0 {
		return errors.New("config: victoriametrics.timeout must be positive")
	}

	if c.RateLimit.CheckRPM <= 0 {
		return errors.New("config: ratelimit.check_rpm must be positive")
	}

	if c.RateLimit.IPRPM <= 0 {
		return errors.New("config: ratelimit.ip_rpm must be positive")
	}

	if len(c.Checks) == 0 {
		return errors.New("config: at least one check must be configured")
	}

	for id := range c.Checks {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("config: check id %q is not a valid uuid: %w", id, err)
		}
	}

	return nil
}
