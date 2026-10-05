# MikroTik RouterOS

[MikroTik RouterOS](https://mikrotik.com/software) is a router operating system used
primarily on MikroTik network hardware. Among its many features is scripting support
and a scheduler.

The commands use the RouterOS v7 slash-separated syntax (`/tool/fetch`). On
RouterOS v6, use the space-separated form (`/tool fetch`).

First, create a script in WebFig, **System › Scripts › Add New**. Use the following
parameters:

* Name: `ping` (example, you can use a different name)
* Policy: `read`, `test`
* Source: `/tool/fetch url="<ping_url>" output=none`

Then, create a schedule in WebFig, **System › Scheduler › Add New**. Use parameters:

* Interval: `00:01:00` (one minute)
* Policy: `read`, `test`
* On Event: `ping` (the name of the script from the previous step)

Notes:

* The `output=none` parameter tells the system to discard response body. Without
  this parameter, the system will save response body to a file, which will additionally
  require the `write` policy.
* The `/tool/fetch` utility supports HTTPS URLs but does not verify TLS
  certificates by default. You can add `check-certificate=yes` parameter to
  require a valid TLS certificate. Note that RouterOS ships with no root CA
  certificates, so you will also need to load these.
* [Here's the full list of options](https://help.mikrotik.com/docs/spaces/ROS/pages/8978525/Fetch)
  supported by `/tool/fetch`.
