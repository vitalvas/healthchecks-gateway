# healthchecks-gateway

A self-hosted gateway that accepts healthchecks.io-style ping requests and
records each ping as a metric in VictoriaMetrics (vmsingle).

Clients (cron jobs, scripts, services) send a ping to signal success, failure,
or the start of a job. The gateway translates every ping into a single
Prometheus sample with an explicit event timestamp and pushes it to
VictoriaMetrics for storage, alerting, and dashboards.

## How it works

```mermaid
flowchart LR
    client["Client (cron / script)"] -->|"GET/POST /ping/{id}"| gw["healthchecks-gateway"]
    gw -->|"POST /api/v1/import/prometheus"| vm["VictoriaMetrics (vmsingle)"]
```

For every ping to a configured check, the gateway pushes one line in Prometheus
exposition format:

```text
healthcheck_event{check="<uuid>",event="<event>",<labels>} 1 <timestamp_ms>
```

- The metric name is always `healthcheck_event`.
- The sample value is always `1`.
- The sample timestamp is the time the ping was received, in milliseconds.
- `<labels>` are the static labels configured for the check.

## Endpoints

All endpoints accept `GET`, `POST`, and `HEAD`. The `{id}` segment must be a
valid UUID.

| Endpoint | `event` label | Extra label |
| --- | --- | --- |
| `/ping/{id}` | `success` | - |
| `/ping/{id}/fail` | `fail` | - |
| `/ping/{id}/start` | `start` | - |
| `/ping/{id}/0` | `success` | `exit_code="0"` |
| `/ping/{id}/{1-255}` | `fail` | `exit_code="<n>"` |

### Run id

Any endpoint accepts an optional `rid` query parameter carrying a
client-generated UUID, used to pair a `start` ping with its completing
`success`/`fail` ping:

```text
/ping/{id}/start?rid=<uuid>
/ping/{id}?rid=<uuid>
```

When present, `rid` must be a valid UUID and is added to the metric as a
`rid="<uuid>"` label. This lets you compute a job's duration in VictoriaMetrics
by matching the `start` and completion samples that share the same `rid`.

### Named pings

A check can accept named sub-pings under a slug segment, which adds a
`name="<slug>"` label to the metric. The same suffixes apply:

```text
/ping/{id}/{name}
/ping/{id}/{name}/fail
/ping/{id}/{name}/start
/ping/{id}/{name}/{0-255}
```

Named pings are controlled per check by the `names` setting (see Configuration):
when `names` is omitted the named pings are rejected, when it is an empty list any
name is allowed, and when it lists names only those are allowed. A disallowed name
is answered with `200 OK (not found)` and logged.

### Request body

A `POST` request may carry a body, but the gateway does not store it. The body
size is limited to 1 KiB; a larger body is rejected with
`413 Request Entity Too Large`.

### Rate limiting

Requests are rate limited with a one-minute fixed window. Exceeding a limit
returns `429 Too Many Requests` and nothing is pushed.

| Limit | Bucket | Default |
| --- | --- | --- |
| Per source IP | `ip:<ip>` (IPv4 `/32`, IPv6 `/128`) | 50 rpm |
| Per check | `ping:<uuid>` | 10 rpm |

The per-IP limit is enforced first, before the check is resolved, so it also
caps requests to unknown UUIDs. The per-check limit applies only to known
checks. When a limit is exceeded the bucket is locked out for the rest of the
window.

The source IP is taken from the connection's remote address, or from
`X-Forwarded-For` / `X-Real-IP` when the direct peer is a trusted proxy. Trusted
proxies are detected automatically: requests arriving from loopback and private
networks (RFC 1918, RFC 4193, RFC 6598 CGNAT, and loopback ranges) are trusted,
so a reverse proxy on the local host or private network works without extra
configuration.

### Responses

| Situation | Status | Body |
| --- | --- | --- |
| Known check, ping recorded | `200` | `OK` |
| Unknown check UUID | `200` | `OK (not found)` |
| Exit code outside `0-255` | `400` | `invalid exit code` |
| Invalid `rid` (not a UUID) | `400` | `invalid rid` |
| Request body over 1 KiB | `413` | `request body too large` |
| Rate limit exceeded | `429` | `rate limit exceeded` |

Unknown UUIDs return `200` on purpose so a misconfigured client does not treat
the ping as a transport failure and retry. The missing UUID is logged.

A push failure to VictoriaMetrics is logged but still returns `200 OK`; the push
is best-effort so a storage outage does not cause clients to retry.

## Configuration

The gateway is configured with a single YAML file. See
[`config.example.yaml`](config.example.yaml) for a complete example.

| Key | Description | Default |
| --- | --- | --- |
| `listen` | HTTP listen address. | `:8080` |
| `victoriametrics.url` | Base URL of the vmsingle instance. | required |
| `victoriametrics.timeout` | Timeout for each push request. | `5s` |
| `ratelimit.check_rpm` | Per-check limit, requests per minute. | `10` |
| `ratelimit.ip_rpm` | Per-source-IP limit, requests per minute. | `50` |
| `checks.<uuid>.labels` | Static labels added to the check's events. | - |
| `checks.<uuid>.names` | Allowed named pings; omitted rejects, empty any. | - |

At least one check must be configured, and each check key must be a valid UUID.
Both rate limits must be positive.

## Running

```sh
healthchecks-gateway --config config.yaml
```

| Flag | Description |
| --- | --- |
| `--config`, `-c` | Path to the YAML configuration file (required). |

The process serves until it receives `SIGINT` or `SIGTERM`, then shuts the HTTP
server down gracefully.

## Example

With the check `f81d4fae-7dec-11d0-a765-00a0c91e6bf6` configured with labels
`service=api` and `env=prod`, a successful ping:

```sh
curl http://localhost:8080/ping/f81d4fae-7dec-11d0-a765-00a0c91e6bf6
```

pushes:

```text
healthcheck_event{check="<uuid>",env="prod",event="success",service="api"} 1 <ts>
```

A job reporting its shell exit status:

```sh
curl http://localhost:8080/ping/f81d4fae-7dec-11d0-a765-00a0c91e6bf6/$?
```

pushes `event="success"` with `exit_code="0"` on success, or `event="fail"`
with the non-zero `exit_code` on failure.
