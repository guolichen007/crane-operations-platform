#!/usr/bin/env bash
# No ACME issuance, production TLS writes, reload or timer enable in this installer.
set -Eeuo pipefail
set +x
umask 077
readonly VERSION=3.1.6
readonly COMMIT=807da6498377ee5e0cf43a78091f46f12dc59a89
readonly TAG_OBJECT=c6e7807f8e2573bf8b02b71da186b4022afafa56
readonly SIGNERS_SHA=8522e614de31e4e36d1c11e45d90721013b37f91bc3d9aa437fe1844d83400fa
readonly SCRIPT_SHA=c7d68b021cfd6380ea83a82962abde5b484779fee0b97d38681dfa1396bbc8d7
readonly VENDOR=/opt/u-dao/vendor/acme.sh/3.1.6
readonly ACME_HOME=${ACME_HOME:-/srv/u-dao/secrets/crane-ops/acme}
readonly WEBROOT=/srv/u-dao/platform/ingress/acme-webroot
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
mode=${1:---install}
[[ $# -le 1 && $mode =~ ^--(install|verify-only)$ ]] || exit 1
[[ $mode == --verify-only || $EUID == 0 ]] || exit 1
provision_locked=0
renewal_lock() {
  [[ -f $ACME_HOME/.renew.lock && ! -L $ACME_HOME/.renew.lock && $(stat -c %u "$ACME_HOME/.renew.lock") == 0 && $(stat -c %a "$ACME_HOME/.renew.lock") == 600 ]] || exit 1
  exec 9<"$ACME_HOME/.renew.lock"
  flock -n 9 || { printf 'ACME_PROVISION=REFUSED reason=renewal-lock\n'; exit 1; }
  provision_locked=1
}
if [[ $mode == --install ]]; then
  service_state=$(systemctl is-active u-dao-crane-cert-renew.service 2>/dev/null || true)
  [[ $service_state == inactive || $service_state == unknown ]] || { printf 'ACME_PROVISION=REFUSED reason=service-active\n'; exit 1; }
  if systemctl is-active --quiet u-dao-crane-cert-renew.timer || systemctl is-enabled --quiet u-dao-crane-cert-renew.timer; then exit 1; fi
  [[ $(realpath -m "$ACME_HOME") == "$ACME_HOME" ]] || exit 1
  if [[ -e $ACME_HOME/.renew.lock || -L $ACME_HOME/.renew.lock ]]; then renewal_lock; fi
fi
temp=$(mktemp -d)
trap '[[ -z ${temp:-} || ! -d $temp ]] || rm -rf -- "$temp"' EXIT
git -c core.autocrlf=false -c core.hooksPath=/dev/null clone --depth 1 --branch "$VERSION" --no-checkout https://github.com/acmesh-official/acme.sh.git "$temp/source" >/dev/null 2>&1
git -C "$temp/source" show "$VERSION:allowed_signers" >"$temp/allowed_signers"
[[ $(sha256sum "$temp/allowed_signers" | cut -d' ' -f1) == "$SIGNERS_SHA" ]]
git -C "$temp/source" -c gpg.ssh.allowedSignersFile="$temp/allowed_signers" verify-tag "$VERSION" >/dev/null 2>&1
[[ $(git -C "$temp/source" rev-parse "$VERSION^{}") == "$COMMIT" ]]
[[ $(git -C "$temp/source" rev-parse "refs/tags/$VERSION") == "$TAG_OBJECT" ]]
git -C "$temp/source" show "$VERSION:acme.sh" >"$temp/acme.sh"
[[ $(sha256sum "$temp/acme.sh" | cut -d' ' -f1) == "$SCRIPT_SHA" ]]
printf 'ACME_TAG_VERIFY=PASS ACME_VERSION=%s ACME_COMMIT=%s\n' "$VERSION" "$COMMIT"
[[ $mode != --verify-only ]] || exit 0
# Existing active automation must be paused by the operator, never silently replaced.
if systemctl is-active --quiet u-dao-crane-cert-renew.timer || systemctl is-enabled --quiet u-dao-crane-cert-renew.timer; then exit 1; fi
if [[ -e $VENDOR ]]; then
  [[ ! -L $VENDOR && $(sha256sum "$VENDOR/acme.sh" | cut -d' ' -f1) == "$SCRIPT_SHA" ]]
else
  install -d -m 0755 "$(dirname "$VENDOR")"
  install -d -m 0700 "$temp/vendor"
  git -C "$temp/source" archive "$VERSION" | tar -x -C "$temp/vendor"
  printf '%s\n' "$COMMIT" >"$temp/vendor/UPSTREAM-COMMIT"
  find "$temp/vendor" -type f -exec chmod 0444 {} +
  find "$temp/vendor" -type d -exec chmod 0555 {} +
  chmod 0555 "$temp/vendor/acme.sh"
  # cp across filesystems, then rename a sibling; never install unverified upstream code.
  staging=$(mktemp -d "$(dirname "$VENDOR")/.verified.XXXXXX")
  cp -a "$temp/vendor/." "$staging/"
  chmod 0555 "$staging"
  mv -T -- "$staging" "$VENDOR"
fi
for path in "$ACME_HOME" "$WEBROOT"; do [[ $(realpath -m "$path") == "$path" ]]; done
install -d -o root -g root -m 0700 "$ACME_HOME" "$ACME_HOME/certs" "$ACME_HOME/deploy"
install -d -o root -g root -m 0755 "$WEBROOT" "$WEBROOT/.well-known/acme-challenge"
if [[ ! -e $ACME_HOME/.renew.lock ]]; then (set -o noclobber; : >"$ACME_HOME/.renew.lock") 2>/dev/null || true; fi
if [[ $provision_locked == 0 ]]; then renewal_lock; fi
install -m 0755 "$root/deploy/scripts/cert-renew.sh" /usr/local/sbin/u-dao-crane-cert-renew
install -d -m 0755 /usr/local/libexec
install -m 0755 "$root/deploy/scripts/cert-state.py" /usr/local/libexec/u-dao-crane-cert-state
install -m 0644 "$root/deploy/systemd/u-dao-crane-cert-renew.service" /etc/systemd/system/
install -m 0644 "$root/deploy/systemd/u-dao-crane-cert-renew.timer" /etc/systemd/system/
systemctl daemon-reload
if systemctl is-enabled --quiet u-dao-crane-cert-renew.timer; then exit 1; fi
printf 'ACME_PROVISION=PASS TIMER=DISABLED\n'
