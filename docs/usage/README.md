# Pinging reliability tips

A ping travels over the network, so it can be slow or fail on its own. These
tips keep the pinging code from interfering with the job and from raising false
alarms.

`<ping_url>` is a placeholder for a check's ping URL.

## Set a request timeout

Cap how long a ping may take so a stuck request cannot delay or block the job.
This matters most for a start ping and for a long-running worker that pings after
each item. With curl, use `-m`:

```bash
curl -m 10 <ping_url>
```

## Retry transient failures

Let the client retry a few times so a brief network blip is not reported as a
failure. With curl, use `--retry`:

```bash
curl --retry 5 <ping_url>
```

## Handle ping errors

Decide how a failed ping is handled. A ping that fails should not abort the job
or change its exit status; keep the ping's own success separate from the job's
outcome.
