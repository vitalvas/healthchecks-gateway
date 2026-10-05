package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestLoad(t *testing.T) {
	t.Run("valid full config", func(t *testing.T) {
		path := writeConfig(t, `
listen: "127.0.0.1:9000"
victoriametrics:
  url: "http://vm.example.com:8428"
  timeout: 10s
ratelimit:
  check_rpm: 20
  ip_rpm: 100
checks:
  f81d4fae-7dec-11d0-a765-00a0c91e6bf6:
    labels:
      service: api
      env: prod
`)

		cfg, err := Load(path)
		require.NoError(t, err)

		assert.Equal(t, "127.0.0.1:9000", cfg.Listen)
		assert.Equal(t, "http://vm.example.com:8428", cfg.VictoriaMetrics.URL)
		assert.Equal(t, 10*time.Second, cfg.VictoriaMetrics.Timeout)
		assert.Equal(t, 20, cfg.RateLimit.CheckRPM)
		assert.Equal(t, 100, cfg.RateLimit.IPRPM)

		check, ok := cfg.Checks["f81d4fae-7dec-11d0-a765-00a0c91e6bf6"]
		require.True(t, ok)
		assert.Equal(t, map[string]string{"service": "api", "env": "prod"}, check.Labels)
	})

	t.Run("applies defaults", func(t *testing.T) {
		path := writeConfig(t, `
victoriametrics:
  url: "http://127.0.0.1:8428"
checks:
  f81d4fae-7dec-11d0-a765-00a0c91e6bf6: {}
`)

		cfg, err := Load(path)
		require.NoError(t, err)

		assert.Equal(t, ":8080", cfg.Listen)
		assert.Equal(t, 5*time.Second, cfg.VictoriaMetrics.Timeout)
		assert.Equal(t, 10, cfg.RateLimit.CheckRPM)
		assert.Equal(t, 50, cfg.RateLimit.IPRPM)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
		require.Error(t, err)
	})

	t.Run("invalid yaml", func(t *testing.T) {
		path := writeConfig(t, "listen: [unterminated")
		_, err := Load(path)
		require.Error(t, err)
	})
}

func TestValidate(t *testing.T) {
	base := func() *Config {
		return &Config{
			Listen: ":8080",
			VictoriaMetrics: VictoriaMetricsConfig{
				URL:     "http://127.0.0.1:8428",
				Timeout: 5 * time.Second,
			},
			RateLimit: RateLimitConfig{
				CheckRPM: 10,
				IPRPM:    50,
			},
			Checks: map[string]Check{
				"f81d4fae-7dec-11d0-a765-00a0c91e6bf6": {},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:   "valid",
			mutate: func(*Config) {},
		},
		{
			name:    "empty listen",
			mutate:  func(c *Config) { c.Listen = "" },
			wantErr: "listen must not be empty",
		},
		{
			name:    "empty url",
			mutate:  func(c *Config) { c.VictoriaMetrics.URL = "" },
			wantErr: "url must not be empty",
		},
		{
			name:    "bad url scheme",
			mutate:  func(c *Config) { c.VictoriaMetrics.URL = "ftp://host" },
			wantErr: "must be http or https",
		},
		{
			name:    "unparsable url",
			mutate:  func(c *Config) { c.VictoriaMetrics.URL = "http://[::1" },
			wantErr: "is invalid",
		},
		{
			name:    "zero timeout",
			mutate:  func(c *Config) { c.VictoriaMetrics.Timeout = 0 },
			wantErr: "timeout must be positive",
		},
		{
			name:    "zero check rpm",
			mutate:  func(c *Config) { c.RateLimit.CheckRPM = 0 },
			wantErr: "check_rpm must be positive",
		},
		{
			name:    "zero ip rpm",
			mutate:  func(c *Config) { c.RateLimit.IPRPM = 0 },
			wantErr: "ip_rpm must be positive",
		},
		{
			name:    "no checks",
			mutate:  func(c *Config) { c.Checks = nil },
			wantErr: "at least one check",
		},
		{
			name:    "invalid check uuid",
			mutate:  func(c *Config) { c.Checks = map[string]Check{"not-a-uuid": {}} },
			wantErr: "is not a valid uuid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(cfg)

			err := cfg.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCheckAllowsName(t *testing.T) {
	tests := []struct {
		name  string
		names *[]string
		input string
		want  bool
	}{
		{name: "nil rejects", names: nil, input: "backup", want: false},
		{name: "empty allows any", names: &[]string{}, input: "backup", want: true},
		{name: "listed allowed", names: &[]string{"backup", "sync"}, input: "sync", want: true},
		{name: "unlisted rejected", names: &[]string{"backup"}, input: "sync", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := Check{Names: tt.names}
			assert.Equal(t, tt.want, check.AllowsName(tt.input))
		})
	}
}
