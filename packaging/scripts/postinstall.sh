#!/bin/sh
systemctl daemon-reload
systemctl enable healthchecks-gateway.service

if systemctl is-enabled --quiet healthchecks-gateway.service; then
    systemctl restart healthchecks-gateway.service || true
fi
