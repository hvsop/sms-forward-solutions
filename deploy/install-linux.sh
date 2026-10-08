#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run this installer as root.' >&2; exit 1; }
[ "$(uname -m)" = x86_64 ] || { echo 'This package requires Linux x86_64.' >&2; exit 1; }
cd "$(dirname "$0")"
sha256sum -c SHA256SUMS
getent group private-sms >/dev/null || groupadd --system private-sms
id private-sms >/dev/null 2>&1 || useradd --system --gid private-sms --home-dir /var/lib/private-sms --no-create-home --shell /sbin/nologin private-sms
install -d -m 0755 /opt/private-sms /opt/private-sms/bin
install -d -o root -g private-sms -m 0750 /etc/private-sms
install -d -o private-sms -g private-sms -m 0700 /var/lib/private-sms
install -m 0755 bin/private-sms /opt/private-sms/bin/private-sms.new
mv /opt/private-sms/bin/private-sms.new /opt/private-sms/bin/private-sms
install -m 0644 deploy/private-sms.service /etc/systemd/system/private-sms.service
systemctl daemon-reload
printf '%s\n' 'Installed. Preserve or create /etc/private-sms/server.env before starting.' 'For migration, copy the original config, APNs key, and stopped data directory.' 'Start with: systemctl enable --now private-sms'
