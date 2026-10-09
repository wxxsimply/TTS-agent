#!/usr/bin/env bash
set -Eeuo pipefail

mode="${1:-}"
deploy_user="${DEPLOY_USER:-ttsdeploy}"
root_keys="/root/.ssh/authorized_keys"
hardening_file="/etc/ssh/sshd_config.d/99-tts-agent-hardening.conf"

if [[ "$mode" == "prepare" ]]; then
  if [[ ! -s "$root_keys" ]]; then
    echo "root authorized_keys is missing; refusing to prepare alternate login" >&2
    exit 1
  fi
  if ! id "$deploy_user" >/dev/null 2>&1; then
    adduser --disabled-password --gecos '' "$deploy_user"
  fi
  install -d -m 700 -o "$deploy_user" -g "$deploy_user" "/home/$deploy_user/.ssh"
  install -m 600 -o "$deploy_user" -g "$deploy_user" "$root_keys" "/home/$deploy_user/.ssh/authorized_keys"
  printf '%s ALL=(ALL) NOPASSWD: ALL\n' "$deploy_user" > "/etc/sudoers.d/90-$deploy_user"
  chmod 440 "/etc/sudoers.d/90-$deploy_user"
  visudo -cf "/etc/sudoers.d/90-$deploy_user" >/dev/null
  echo "alternate SSH user prepared: $deploy_user"
  exit 0
fi

if [[ "$mode" == "lock-root" ]]; then
  if ! id "$deploy_user" >/dev/null 2>&1; then
    echo "alternate deploy user is missing" >&2
    exit 1
  fi
  cat > "$hardening_file" <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
PermitRootLogin no
PubkeyAuthentication yes
EOF
  chmod 644 "$hardening_file"
  sshd -t
  systemctl reload ssh
  echo "SSH password and direct root login disabled"
  exit 0
fi

echo "usage: $0 prepare|lock-root" >&2
exit 2
