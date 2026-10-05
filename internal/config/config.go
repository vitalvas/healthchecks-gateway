// Package config loads and validates the gateway configuration from a YAML file.
package config

import (
	"fmt"
	"slices"
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
	URL     string        `yaml:"url" default:"http://127.0.0.1:8428"`
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

	// Names controls slug-based named pings under /ping/<uuid>/<name>. When nil
	// (the key is omitted), named pings are rejected. When an empty slice, any
	// name is allowed. When populated, only the listed names are allowed.
	Names *[]string `yaml:"names"`
}

// AllowsName reports whether a named ping with the given slug is allowed for the
// check: rejected when Names is nil, allowed for any name when Names is empty,
// and otherwise allowed only when the name is listed.
func (c Check) AllowsName(name string) bool {
	if c.Names == nil {
		return false
	}

	if len(*c.Names) == 0 {
		return true
	}

	return slices.Contains(*c.Names, name)
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

// validate rejects a check key that is not a valid UUID. Every other field has a
// default, so an empty config is valid.
func (c *Config) validate() error {
	for id := range c.Checks {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("config: check id %q is not a valid uuid: %w", id, err)
		}
	}

	return nil
}
