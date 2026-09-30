#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Run this reviewed installer as root.' >&2; exit 1; }
source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ci_uid="$(id -u mm-ci)"
[[ "$(getent passwd mm-ci | cut -d: -f6)" == /var/lib/media-manager-ci ]]
[[ -S "/run/user/${ci_uid}/docker.sock" ]]
install -d -m 711 /var/lib/media-manager-deploy
install -d -m 730 -o root -g mm-ci /var/lib/media-manager-deploy/inbox
install -d -m 750 -o root -g mm-ci /var/lib/media-manager-deploy/results
install -d -m 755 /usr/local/libexec
install -m 755 "${source_dir}/runner-hook.sh" /usr/local/libexec/mm-ci-job-started.sh
install -m 755 "${source_dir}/runner-hook.sh" /usr/local/libexec/mm-ci-job-completed.sh
install -m 755 "${source_dir}/deploy-broker.py" /usr/local/libexec/media-manager-deploy.py
install -m 644 "${source_dir}/deploy.service" /etc/systemd/system/media-manager-github-deploy.service
install -m 644 "${source_dir}/deploy.path" /etc/systemd/system/media-manager-github-deploy.path
install -m 644 "${source_dir}/runner.service" /var/lib/media-manager-ci/.config/systemd/user/mm-github-runner.service
install -d -m 755 "/etc/systemd/system/user-${ci_uid}.slice.d" "/etc/systemd/system/user@${ci_uid}.service.d"
cat >"/etc/systemd/system/user-${ci_uid}.slice.d/resources.conf" <<'UNIT'
[Slice]
CPUQuota=500%
CPUWeight=25
MemoryHigh=10G
MemoryMax=12G
TasksMax=4096
IOWeight=25
UNIT
cat >"/etc/systemd/system/user@${ci_uid}.service.d/delegate.conf" <<'UNIT'
[Service]
Delegate=cpu cpuset io memory pids
UNIT
systemctl daemon-reload
systemctl enable --now media-manager-github-deploy.path
runuser -u mm-ci -- env "XDG_RUNTIME_DIR=/run/user/${ci_uid}" \
  "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${ci_uid}/bus" systemctl --user daemon-reload
runuser -u mm-ci -- env "XDG_RUNTIME_DIR=/run/user/${ci_uid}" \
  "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${ci_uid}/bus" systemctl --user enable --now mm-github-runner.service
