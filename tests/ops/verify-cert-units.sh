#!/usr/bin/env bash
# Real systemd parser in an isolated filesystem; no daemon reload/start/enable.
set -Eeuo pipefail
umask 077
root=$(mktemp -d)
trap 'rm -rf -- "$root"' EXIT
mkdir -p "$root/etc/systemd/system" "$root/usr/local/sbin" "$root/bin"
cp deploy/systemd/u-dao-crane-cert-renew.{service,timer} "$root/etc/systemd/system/"
chmod 0644 "$root/etc/systemd/system/"*
install -m 0755 deploy/scripts/cert-renew.sh "$root/usr/local/sbin/u-dao-crane-cert-renew"
install -m 0755 /bin/true "$root/bin/true"
for target in sysinit basic shutdown network-online timers; do
  printf '[Unit]\nDescription=Isolated dependency stub\nDefaultDependencies=no\n' >"$root/etc/systemd/system/$target.target"
done
printf '[Unit]\nDescription=Isolated Docker dependency\nDefaultDependencies=no\n[Service]\nType=oneshot\nExecStart=/bin/true\n' >"$root/etc/systemd/system/docker.service"
# Older systemd versions do not support verify --root. Relocate only ExecStart
# in this private validation copy; the original absolute command is contract-tested.
sed -i "s#ExecStart=/usr/local/sbin/u-dao-crane-cert-renew --run#ExecStart=$root/usr/local/sbin/u-dao-crane-cert-renew --run#" "$root/etc/systemd/system/u-dao-crane-cert-renew.service"
sed -i "s#ExecStart=/bin/true#ExecStart=$root/bin/true#" "$root/etc/systemd/system/docker.service"
SYSTEMD_UNIT_PATH="$root/etc/systemd/system" systemd-analyze --man=no verify "$root/etc/systemd/system/u-dao-crane-cert-renew.service" "$root/etc/systemd/system/u-dao-crane-cert-renew.timer"
printf 'SYSTEMD_ANALYZE_VERIFY=PASS\n'
