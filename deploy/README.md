# TTS Agent server operations

Production directory: `/opt/tts-agent`

## Service status

```bash
cd /opt/tts-agent
docker compose --env-file .env -f docker-compose.prod.yml ps
curl --fail http://127.0.0.1/api/health/ready
```

## Backup and restore

Backups run daily at 03:15 Asia/Shanghai and are retained for seven days.

```bash
sudo /usr/local/sbin/tts-agent-backup
sudo /usr/local/sbin/tts-agent-restore-drill
```

Restore a backup to a separate database first. The restore command refuses to overwrite `tts_agent`.

```bash
sudo /usr/local/sbin/tts-agent-restore \
  /var/backups/tts-agent/postgres/tts_agent_YYYYMMDDTHHMMSSZ.dump \
  tts_agent_recovery_check
```

## Monitoring

```bash
sudo systemctl status tts-agent-monitor.timer
sudo journalctl -u tts-agent-monitor.service -n 50 --no-pager
sudo cat /var/lib/tts-agent-monitor/last-status.json
sudo /usr/local/sbin/tts-agent-audit-secrets
```

## Release and rollback

Upload an image archive containing `tts-agent-backend:latest` and
`tts-agent-frontend:latest`, then run:

```bash
sudo /usr/local/sbin/tts-agent-release /opt/tts-agent/deployment-images.tar
sudo /usr/local/sbin/tts-agent-rollback
```

The release command creates rollback image tags, takes a database backup,
recreates only the application containers, and automatically rolls back when
the health check fails.

## Smoke test

```bash
sudo TEST_WORKER_RECOVERY=1 /opt/tts-agent/deploy/smoke-test.sh
```

The test covers registration, login, token refresh, conversation creation,
idempotent async submission, queued cancellation, worker restart recovery,
result retrieval, and message persistence. Remove the generated
`smoke-*@example.test` user after reviewing the result.
