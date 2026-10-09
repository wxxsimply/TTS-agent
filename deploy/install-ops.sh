#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"

install -m 700 "$APP_DIR/deploy/backup-postgres.sh" /usr/local/sbin/tts-agent-backup
install -m 700 "$APP_DIR/deploy/restore-postgres.sh" /usr/local/sbin/tts-agent-restore
install -m 700 "$APP_DIR/deploy/restore-drill.sh" /usr/local/sbin/tts-agent-restore-drill
install -m 700 "$APP_DIR/deploy/monitor.sh" /usr/local/sbin/tts-agent-monitor
install -m 700 "$APP_DIR/deploy/release.sh" /usr/local/sbin/tts-agent-release
install -m 700 "$APP_DIR/deploy/rollback.sh" /usr/local/sbin/tts-agent-rollback
install -m 700 "$APP_DIR/deploy/audit-secrets.sh" /usr/local/sbin/tts-agent-audit-secrets
install -m 700 "$APP_DIR/deploy/configure-admin.sh" /usr/local/sbin/tts-agent-configure-admin
install -m 700 "$APP_DIR/deploy/eval-gate.sh" /usr/local/sbin/tts-agent-eval

install -m 644 "$APP_DIR/deploy/systemd/tts-agent-backup.service" /etc/systemd/system/tts-agent-backup.service
install -m 644 "$APP_DIR/deploy/systemd/tts-agent-backup.timer" /etc/systemd/system/tts-agent-backup.timer
install -m 644 "$APP_DIR/deploy/systemd/tts-agent-monitor.service" /etc/systemd/system/tts-agent-monitor.service
install -m 644 "$APP_DIR/deploy/systemd/tts-agent-monitor.timer" /etc/systemd/system/tts-agent-monitor.timer
install -m 644 "$APP_DIR/deploy/systemd/tts-agent-eval.service" /etc/systemd/system/tts-agent-eval.service
install -m 644 "$APP_DIR/deploy/systemd/tts-agent-eval.timer" /etc/systemd/system/tts-agent-eval.timer

systemctl daemon-reload
systemctl enable --now tts-agent-backup.timer tts-agent-monitor.timer tts-agent-eval.timer
systemctl start tts-agent-backup.service
systemctl start tts-agent-monitor.service
systemctl list-timers --all 'tts-agent-*' --no-pager
