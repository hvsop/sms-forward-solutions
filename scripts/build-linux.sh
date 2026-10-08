#!/bin/sh
set -eu
SMS_PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SMS_OUTPUT_DIR=${1:-"$SMS_PROJECT_ROOT/dist"}
mkdir -p "$SMS_OUTPUT_DIR"
SMS_OUTPUT_DIR=$(CDPATH= cd -- "$SMS_OUTPUT_DIR" && pwd)
SMS_PACKAGE_DIR="$SMS_OUTPUT_DIR/private-sms-linux-amd64"
mkdir -p "$SMS_PACKAGE_DIR/bin" "$SMS_PACKAGE_DIR/deploy"
cd "$SMS_PROJECT_ROOT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -ldflags='-s -w' -o "$SMS_PACKAGE_DIR/bin/private-sms" ./server
cp deploy/private-sms.service deploy/nginx.conf deploy/renew-nginx.sh "$SMS_PACKAGE_DIR/deploy/"
cp deploy/install-linux.sh "$SMS_PACKAGE_DIR/install.sh"
cp docs/linux-package.md "$SMS_PACKAGE_DIR/README.md"
chmod 0755 "$SMS_PACKAGE_DIR/install.sh" "$SMS_PACKAGE_DIR/deploy/renew-nginx.sh"
cd "$SMS_PACKAGE_DIR"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum bin/private-sms deploy/private-sms.service deploy/nginx.conf deploy/renew-nginx.sh install.sh README.md > SHA256SUMS
else
    shasum -a 256 bin/private-sms deploy/private-sms.service deploy/nginx.conf deploy/renew-nginx.sh install.sh README.md > SHA256SUMS
fi
COPYFILE_DISABLE=1 tar -czf "$SMS_OUTPUT_DIR/private-sms-linux-amd64.tar.gz" -C "$SMS_OUTPUT_DIR" private-sms-linux-amd64
printf '%s\n' "$SMS_OUTPUT_DIR/private-sms-linux-amd64.tar.gz"
