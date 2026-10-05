# How to Monitor Systemd Tasks with healthchecks-gateway

healthchecks-gateway can monitor your Systemd scheduled tasks and notify you
when they don't run at expected times. Assuming curl or wget is available, you
will not need to install new software on your servers.

healthchecks-gateway monitoring works by listening for "start" and "success"
signals sent as HTTP requests by the monitored task. When healthchecks-gateway
does not receive the HTTP request at the expected time, it notifies you. This
monitoring technique, also called "heartbeat monitoring", can detect various
failure modes:

* The whole machine goes down (power outage, hardware failure, somebody trips on
  cables, etc.).
* Systemd does not start the task because of an invalid configuration.
* The task exits with a non-zero exit code.
* The task runs at the wrong time or keeps running for an abnormally long time.

Each Systemd scheduled task is defined by two files:

* The `.service` file describes the command to run, the system user to run it as,
  the environment variables to set, and what other services must already be running.
* The `.timer` file contains the task's schedule.

## Using curl

To monitor a task with healthchecks-gateway, you will need to make changes in the
`.service` file. Let's consider a service "copy-media.service" which copies the
`/opt/media` directory to a remote host:

```ini
[Unit]
Description=Copy /opt/media to remote_host
Requires=network-online.target

[Service]
Type=oneshot
ExecStart=rsync -a /opt/media/ remote_user@remote_host:/opt/media/
```

Here's the same service, extended to send a start signal to
healthchecks-gateway before the main command runs, and to report the command's
exit status to healthchecks-gateway after it completes:

```ini
[Unit]
Description=Copy /opt/media to remote_host, with healthchecks-gateway monitoring
Requires=network-online.target

[Service]
Type=oneshot
ExecStartPre=-curl -sS -m 10 --retry 5 <ping_url>/start
ExecStart=rsync -a /opt/media/ remote_user@remote_host:/opt/media/
ExecStopPost=curl -sS -m 10 --retry 5 <ping_url>/${EXIT_STATUS}
```

The `ExecStartPre` command runs before the main process. The "-" prefix in front
of the command is important and tells Systemd to ignore curl failure (timeout or
non-zero exit status), which could otherwise prevent the main command from
running.

The `ExecStopPost` command runs after the main process finishes. Systemd
provides an `$EXIT_STATUS` variable with the exit status of the main process (a
0-255 number). healthchecks-gateway will consider exit status 0 as success, and
anything above 0 as failure.

curl flags:

* `-sS` means "suppress output except errors". This is so that if the curl call fails,
  the error is printed in system logs.
* `-m <seconds>` is the maximum in seconds that the HTTP request is allowed to take.
* `--retry <num>` is how many times curl will retry transient failures
  (timeouts, HTTP 5xx status codes).

This example only requires curl to be installed on the system but does not capture
the command's output.

Read more about [ExecStartPre](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#ExecStartPre=)
and [ExecStopPost](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#ExecStopPost=)
in Systemd documentation.

## Host Heartbeat

To confirm a host is alive, send a plain success ping on a fixed interval with a
oneshot service and a timer, instead of wrapping a task.

Service unit, `healthcheck-heartbeat.service`:

```ini
[Unit]
Description=Host heartbeat
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=root
Group=root
ExecStart=/usr/bin/curl -sS -m 10 --retry 5 <ping_url>
```

Timer unit, `healthcheck-heartbeat.timer`:

```ini
[Unit]
Description=Host heartbeat

[Timer]
OnCalendar=*-*-* *:*:00
AccuracySec=5s
Persistent=false

[Install]
WantedBy=timers.target
```

The timer fires every minute. Enable and start it with
`systemctl enable --now healthcheck-heartbeat.timer`.
