# Pinging from shell scripts

Add a ping to a shell script by making an HTTP request with curl (or wget) at
the right point in the script.

`<ping_url>` is a placeholder for a check's ping URL.

## Simple ping

```bash
curl -sS -m 10 --retry 5 <ping_url>
```

- `-sS` hides the progress meter but still prints errors.
- `-m 10` caps the request time.
- `--retry 5` retries transient failures.

## Report the exit status

Append `$?` to signal the command's result. The gateway reads `0` as success and
`1`-`255` as failure, and keeps the number in an `exit_code` label.

```bash
#!/bin/bash
/usr/bin/certbot renew
curl -sS -m 10 --retry 5 <ping_url>/$?
```

For a pipeline, set `pipefail` so the exit status reflects any failing stage, not
just the last command:

```bash
#!/bin/bash
set -o pipefail
pg_dump somedb | gzip > somedb.sql.gz
curl -sS -m 10 --retry 5 <ping_url>/$?
```

## Signal start and finish

Ping `/start` before the work, then report the result. Pass the same `rid` UUID
on both to pair them:

```bash
#!/bin/bash
rid=$(uuidgen)
curl -sS -m 10 --retry 5 "<ping_url>/start?rid=$rid"

/usr/bin/certbot renew
curl -sS -m 10 --retry 5 "<ping_url>/$??rid=$rid"
```

## Send command output

With HTTP POST the body is accepted but capped at 1 KiB; a larger body is
rejected with `413`. The body is not stored, so send a short summary.

```bash
#!/bin/bash
m=$(/usr/bin/certbot renew 2>&1)
curl -sS -m 10 --retry 5 --data-raw "$m" <ping_url>/$?
```

## Periodic ping from cron

```cron
*/5 * * * * curl -sS -m 10 --retry 5 <ping_url>
```

## Using wget

```bash
wget -q -T 10 -t 5 -O /dev/null <ping_url>
```
