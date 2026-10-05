// Package metrics formats healthcheck events as Prometheus exposition lines and
// pushes them to a VictoriaMetrics (vmsingle) instance.
package metrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// metricName is the single metric emitted for every healthcheck event.
const metricName = "healthcheck_event"

// importPath is the vmsingle endpoint that ingests Prometheus exposition format.
const importPath = "/api/v1/import/prometheus"

// Event is a single healthcheck signal to record.
type Event struct {
	// Check is the healthcheck UUID.
	Check string
	// Event is the event label value (success, fail, start).
	Event string
	// Labels are the static labels configured for the check.
	Labels map[string]string
	// ExitCode, when set, adds an exit_code label. Use nil to omit it.
	ExitCode *int
	// Timestamp is the event time; it becomes the sample timestamp.
	Timestamp time.Time
}

// Client pushes healthcheck events to VictoriaMetrics.
type Client struct {
	url  string
	http *http.Client
}

// NewClient returns a Client that pushes to the vmsingle instance at baseURL
// using the given request timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		url:  fmt.Sprintf("%s%s", strings.TrimRight(baseURL, "/"), importPath),
		http: &http.Client{Timeout: timeout},
	}
}

// Push formats the event and sends it to VictoriaMetrics.
func (c *Client) Push(ctx context.Context, event Event) error {
	line := formatLine(event)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(line))
	if err != nil {
		return fmt.Errorf("metrics: build request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("metrics: push: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("metrics: unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	return nil
}

// formatLine renders a single Prometheus exposition line. The sample value is
// the event time in Unix seconds and the sample timestamp is the same instant in
// milliseconds:
//
//	healthcheck_event{check="...",event="...",<labels>} <ts_sec> <ts_ms>
func formatLine(event Event) string {
	labels := make(map[string]string, len(event.Labels)+2)
	maps.Copy(labels, event.Labels)

	labels["check"] = event.Check
	labels["event"] = event.Event

	if event.ExitCode != nil {
		labels["exit_code"] = strconv.Itoa(*event.ExitCode)
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(metricName)
	b.WriteByte('{')

	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}

		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(labels[k]))
		b.WriteByte('"')
	}

	b.WriteString("} ")
	b.WriteString(strconv.FormatInt(event.Timestamp.Unix(), 10))
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(event.Timestamp.UnixMilli(), 10))

	return b.String()
}

// escapeLabelValue escapes a label value per the Prometheus exposition format:
// backslash, double quote, and newline.
func escapeLabelValue(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
	)

	return replacer.Replace(value)
}
