# GitHub Actions

You can augment your GitHub Actions workflows to report success and
failure to healthchecks-gateway:

```yaml
name: Hourly Housekeeping
on:
  schedule:
    - cron: '15 * * * *'
jobs:
  main-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Running housekeeping tasks..."
  ping-success:
    runs-on: ubuntu-latest
    needs: [main-job]
    steps:
      - run: curl -m 10 --retry 5 ${{ secrets.ping_url }}
  ping-failure:
    runs-on: ubuntu-latest
    if: ${{ failure() }}
    needs: [main-job]
    steps:
      - run: curl -m 10 --retry 5 ${{ secrets.ping_url }}/fail
```

Note how the jobs `ping-success` and `ping-failure` define `main-job` as
their dependency. `ping-success` runs only if `main-job` completes
successfully, and `ping-failure` runs when `main-job` fails.

To avoid exposing the ping URL, it is a good idea to define it
as [a secret](https://docs.github.com/en/actions/security-guides/encrypted-secrets)
and access it via the `secrets` context.

## Using the `workflow_run` Trigger

Alternatively, you can put the pinging logic in a separate workflow,
and configure it to trigger every time your main workflow finishes. The main workflow:

```yaml
name: Hourly Housekeeping
on:
  schedule:
    - cron: '15 * * * *'
jobs:
  main-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Running housekeeping tasks..."
```

And the monitoring workflow:

```yaml
name: Ping healthchecks-gateway
on:
  workflow_run:
    workflows: ['Hourly Housekeeping']
    types: [completed]
jobs:
  ping-success:
    runs-on: ubuntu-latest
    if: ${{ github.event.workflow_run.conclusion == 'success' }}
    steps:
      - run: curl -m 10 --retry 5 ${{ secrets.ping_url }}
  ping-failure:
    runs-on: ubuntu-latest
    if: ${{ github.event.workflow_run.conclusion == 'failure' }}
    steps:
      - run: curl -m 10 --retry 5 ${{ secrets.ping_url }}/fail
```
