#!/usr/bin/env bash
# Root-only, single-host HTTP-01 lifecycle. No upstream output enters the journal.
set -Eeuo pipefail
set +x
umask 077
readonly DOMAIN=crane.youdaozd.com
ACME_HOME=${ACME_HOME:-/srv/u-dao/secrets/crane-ops/acme}
WEBROOT=${WEBROOT:-/srv/u-dao/platform/ingress/acme-webroot}
PRODUCTION_TLS=${PRODUCTION_TLS:-/srv/u-dao/platform/ingress/tls/crane}
ACME_BIN=${ACME_BIN:-/opt/u-dao/vendor/acme.sh/3.1.6/acme.sh}
STATE_TOOL=${CERT_STATE_TOOL:-/usr/local/libexec/u-dao-crane-cert-state}
readonly INGRESS=u-dao-ingress-nginx-1
readonly STAGE="$ACME_HOME/deploy/$DOMAIN"
readonly MARKER="$ACME_HOME/.production-adopted"
readonly PENDING="$ACME_HOME/.promotion-pending"
mode=${1:-}
work= rollback= probe= promoting=0 locked=0
state() { python3 "$STATE_TOOL" "$ACME_HOME" "$PRODUCTION_TLS" "$@" 2>/dev/null; }
result() { printf '%s domain=%s CERT_RENEW_RESULT=%s\n' "$(date -u +%FT%TZ)" "$DOMAIN" "$1"; }
fail() { state diagnostic "${1:-validation}" >/dev/null 2>&1 || true; result FAIL; exit 1; }
safe_path() { [[ $1 == /* && $1 != / && $1 != *[[:space:]]* && $(realpath -m -- "$1") == "$1" ]]; }
disjoint() { [[ $1 != "$2" && $1/ != "$2/"* && $2/ != "$1/"* ]]; }
private_dir() { [[ -d $1 && ! -L $1 && $(stat -c %u "$1") == 0 && $(stat -c %a "$1") == 700 ]]; }
cleanup() {
  [[ -z $probe ]] || rm -f -- "$probe"
  # Never remove recovery material on EXIT: SIGKILL does not run this trap.
  if [[ $locked == 1 && $mode != --check && ! -e $PENDING && ! -L $PENDING && ! -e $ACME_HOME/.renew-critical && ! -L $ACME_HOME/.renew-critical ]]; then state gc >/dev/null 2>&1 || true; fi
  [[ -z $work || $work != "$ACME_HOME"/.work.* ]] || rm -rf -- "$work"
}
trap cleanup EXIT
nginx_test() { docker exec "$INGRESS" nginx -t >/dev/null 2>&1; }
reload() { docker exec "$INGRESS" nginx -s reload >/dev/null 2>&1; }
fingerprint() { openssl x509 -in "$1" -noout -fingerprint -sha256 2>/dev/null | sed 's/.*=//;s/://g'; }
validate_pair() {
  local cert=$1 key=$2 candidate=${3:-0} san cert_pub key_pub
  [[ -f $cert && ! -L $cert && -f $key && ! -L $key ]] || return 1
  python3 - "$cert" <<'PY' >/dev/null 2>&1 || return 1
import pathlib, re, sys
assert re.fullmatch(r'(?:\s*-----BEGIN CERTIFICATE-----[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----\s*)+', pathlib.Path(sys.argv[1]).read_text())
PY
  openssl x509 -in "$cert" -noout >/dev/null 2>&1 || return 1
  san=$(openssl x509 -in "$cert" -noout -ext subjectAltName 2>/dev/null) || return 1
  [[ $(printf '%s\n' "$san" | tail -n +2 | tr -d '[:space:]') == "DNS:$DOMAIN" ]] || return 1
  openssl pkey -in "$key" -passin pass: -noout >/dev/null 2>&1 || return 1
  cert_pub=$(openssl x509 -in "$cert" -pubkey -noout 2>/dev/null | openssl pkey -pubin -outform DER 2>/dev/null | sha256sum) || return 1
  key_pub=$(openssl pkey -in "$key" -passin pass: -pubout -outform DER 2>/dev/null | sha256sum) || return 1
  [[ $cert_pub == "$key_pub" ]] || return 1
  openssl crl2pkcs7 -nocrl -certfile "$cert" 2>/dev/null | openssl pkcs7 -print_certs -noout >/dev/null 2>&1 || return 1
  if [[ $candidate == 1 ]]; then
    openssl rsa -in "$key" -passin pass: -noout -check >/dev/null 2>&1 || return 1
    openssl x509 -in "$cert" -noout -checkend 604800 >/dev/null 2>&1 || return 1
    openssl x509 -in "$cert" -issuer -noout 2>/dev/null | grep -Eq "O[[:space:]]*=[[:space:]]*Let's Encrypt(,|$)" || return 1
    openssl pkey -in "$key" -passin pass: -pubout 2>/dev/null | openssl pkey -pubin -text -noout 2>/dev/null | grep -q 'Public-Key: (2048 bit)' || return 1
    openssl verify -purpose sslserver -verify_hostname "$DOMAIN" -untrusted "$cert" "$cert" >/dev/null 2>&1 || return 1
  fi
}
https_get() { curl --disable --fail --silent --show-error --location --max-redirs 3 --proto '=https' --proto-redir '=https' --noproxy '*' --connect-timeout 5 --max-time 20 "$1" -o "$2" 2>/dev/null; }
health_once() {
  local expected=$1
  https_get "https://$DOMAIN/" "$work/crane" || return 1
  https_get "https://$DOMAIN/realms/crane/.well-known/openid-configuration" "$work/oidc" || return 1
  python3 - "$work/oidc" <<'PY' >/dev/null 2>&1 || return 1
import json, sys
assert json.load(open(sys.argv[1]))['issuer'] == 'https://crane.youdaozd.com/realms/crane'
PY
  timeout 20 openssl s_client -connect "$DOMAIN:443" -servername "$DOMAIN" -verify_hostname "$DOMAIN" -verify_return_error -showcerts </dev/null >"$work/remote" 2>/dev/null || return 1
  [[ $(fingerprint "$work/remote") == "$expected" ]] || return 1
  https_get https://firebot.youdaozd.com/ "$work/firebot" || return 1
  https_get https://firebot.youdaozd.com/health/live "$work/firebot-health" || return 1
}
health() {
  local attempt
  for attempt in 1 2 3 4 5; do
    if health_once "$1"; then return 0; fi
    sleep 2
  done
  return 1
}
install_pair() { state install "$1"; }
critical() { promoting=0; state critical state-policy >/dev/null 2>&1 || true; result CRITICAL; exit 1; }
rollback_exit() {
  promoting=0
  if install_pair "$rollback" && nginx_test && reload && health "$(fingerprint "$rollback/fullchain.pem")"; then
    state finish recovery || critical
    result ROLLED_BACK; exit 1
  fi
  critical
}
abort() { trap - ERR INT TERM; if [[ $promoting == 1 ]]; then rollback_exit; fi; fail; }
trap abort ERR INT TERM
recover_pending() {
  local record name old candidate original_mode adopted
  record=$(state read) || critical
  IFS=$'\t' read -r name old candidate original_mode adopted <<<"$record"
  rollback="$ACME_HOME/deploy/$name"
  validate_pair "$rollback/fullchain.pem" "$rollback/privkey.pem" || critical
  [[ $(fingerprint "$rollback/fullchain.pem") == "$old" ]] || critical
  # Ignore the current (possibly mixed) pair and candidate; only the validated journal
  # can select a recovery source. No CA request in this invocation, even after success.
  if install_pair "$rollback" && nginx_test && reload && health "$old"; then
    state finish recovery || critical
    result RECOVERED_ROLLBACK; exit 3
  fi
  critical
}
adopt_after_health() { if [[ $mode == --adopt ]]; then state adopt "$1" || fail state-policy; fi; }
promote() {
  validate_pair "$STAGE/fullchain.pem" "$STAGE/privkey.pem" 1 || fail
  validate_pair "$PRODUCTION_TLS/fullchain.pem" "$PRODUCTION_TLS/privkey.pem" || fail
  local candidate current
  candidate=$(fingerprint "$STAGE/fullchain.pem")
  current=$(fingerprint "$PRODUCTION_TLS/fullchain.pem")
  if [[ $candidate == "$current" ]]; then
    health "$current" || fail validation
    adopt_after_health "$current"
    result NOOP; return
  fi
  nginx_test || fail
  rollback=$(mktemp -d "$ACME_HOME/deploy/.rollback.XXXXXX")
  cp -- "$PRODUCTION_TLS/fullchain.pem" "$rollback/fullchain.pem"
  cp -- "$PRODUCTION_TLS/privkey.pem" "$rollback/privkey.pem"
  chmod 0600 "$rollback/"*.pem
  local adopted=0
  [[ ! -f $MARKER ]] || adopted=1
  state create "${rollback##*/}" "$current" "$candidate" "$mode" "$adopted" || fail state-policy
  promoting=1
  install_pair "$STAGE" || rollback_exit
  nginx_test || rollback_exit
  reload || rollback_exit
  health "$candidate" || rollback_exit
  # Production renames and the private rollback pair are durable before the journal
  # is removed. Orphans after the removal are handled by validated, locked GC.
  state finish || critical
  promoting=0
  adopt_after_health "$candidate"
  result PROMOTED
}
acme() { ACME_PACKAGED=1 AUTO_UPGRADE=0 state acme "$ACME_BIN" "$@" --server letsencrypt --home "$ACME_HOME" --config-home "$ACME_HOME" --cert-home "$ACME_HOME/certs"; }
domain_policy() {
  # acme.sh sources its state and can execute saved hooks. Reject foreign domains,
  # install destinations or hooks before allowing --issue/--cron to read it.
  python3 - "$ACME_HOME/certs" "$STAGE" "$WEBROOT" <<'PY'
import pathlib, re, sys
root, stage, webroot = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
if not root.exists(): sys.exit(0)
for folder in root.iterdir():
    assert folder.name == 'crane.youdaozd.com' and folder.is_dir() and not folder.is_symlink()
    conf = folder / 'crane.youdaozd.com.conf'
    assert conf.is_file() and not conf.is_symlink()
    values = {}
    for line in conf.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'): continue
        match = re.fullmatch(r"([A-Za-z_][A-Za-z_0-9]*)='([^']*)'", line)
        assert match, 'only literal acme.sh assignments are accepted'
        key, value = match.groups()
        if key.startswith('Le_'): values[key] = value
    assert values.get('Le_Domain') == 'crane.youdaozd.com'
    assert values.get('Le_Alt', '') in ('', 'no')
    assert values.get('Le_Keylength') == '2048'
    assert values.get('Le_Webroot') == webroot
    assert values.get('Le_API') == 'https://acme-v02.api.letsencrypt.org/directory'
    assert values.get('Le_RealKeyPath') == stage + '/privkey.pem'
    assert values.get('Le_RealFullChainPath') == stage + '/fullchain.pem'
    for key, value in values.items():
        if any(word in key.lower() for word in ('hook', 'reloadcmd')) or key in ('Le_RealCertPath', 'Le_RealCACertPath'): assert not value
PY
}
[[ $# == 1 && $mode =~ ^--(bootstrap|adopt|run|check)$ && $EUID == 0 ]] || fail
for path in "$ACME_HOME" "$WEBROOT" "$PRODUCTION_TLS" "$STAGE"; do safe_path "$path" || fail; done
disjoint "$ACME_HOME" "$PRODUCTION_TLS" || fail
disjoint "$ACME_HOME" "$WEBROOT" || fail
disjoint "$WEBROOT" "$PRODUCTION_TLS" || fail
private_dir "$ACME_HOME" || fail
[[ -f $ACME_HOME/.renew.lock && ! -L $ACME_HOME/.renew.lock ]] || fail
exec 9<"$ACME_HOME/.renew.lock"
flock -n 9 || { result LOCKED; exit 1; }
locked=1
work=$(mktemp -d "$ACME_HOME/.work.XXXXXX")
[[ -f $STATE_TOOL && ! -L $STATE_TOOL ]] || fail state-policy
# Recovery wins over candidate validation, adoption, critical latch and ACME.
# Bootstrap also refuses to skip an interrupted production transaction.
if [[ $mode != --check && ( -e $PENDING || -L $PENDING ) ]]; then recover_pending; fi
if [[ $mode != --check ]]; then state gc || critical; fi
if [[ $mode == --check ]]; then
  validate_pair "$PRODUCTION_TLS/fullchain.pem" "$PRODUCTION_TLS/privkey.pem" || fail
  openssl x509 -in "$PRODUCTION_TLS/fullchain.pem" -noout -checkend 0 >/dev/null 2>&1 || fail
  health "$(fingerprint "$PRODUCTION_TLS/fullchain.pem")" || fail
  openssl x509 -in "$PRODUCTION_TLS/fullchain.pem" -issuer -enddate -fingerprint -sha256 -noout 2>/dev/null
  result CHECKED; exit 0
fi
if [[ $mode == --run && -e $ACME_HOME/.renew-critical ]]; then result CRITICAL; exit 1; fi
if [[ $mode == --run && ! -e $MARKER ]]; then result NOOP; exit 0; fi
[[ ! -L $MARKER && -x $ACME_BIN && ! -L $ACME_BIN && $(stat -c %u "$ACME_BIN") == 0 ]] || fail
(( (8#$(stat -c %a "$ACME_BIN") & 8#022) == 0 )) || fail
[[ -d $PRODUCTION_TLS && ! -L $PRODUCTION_TLS && $(stat -c %u "$PRODUCTION_TLS") == 0 ]] || fail
(( (8#$(stat -c %a "$PRODUCTION_TLS") & 8#022) == 0 )) || fail
install -d -m 0700 "$ACME_HOME/deploy" "$STAGE"
private_dir "$STAGE" || fail
case $mode in
  --bootstrap)
    domain_policy >/dev/null 2>&1 || fail state-policy
    [[ -d $WEBROOT && ! -L $WEBROOT ]] || fail
    install -d -m 0755 "$WEBROOT/.well-known/acme-challenge"
    token=$(openssl rand -hex 24)
    probe="$WEBROOT/.well-known/acme-challenge/$token"
    printf '%s' "$token" >"$probe"; chmod 0644 "$probe"
    ip=$(curl --disable --fail --silent --show-error --proto '=http' --noproxy '*' --connect-timeout 5 --max-time 20 -w '%{remote_ip}' "http://$DOMAIN/.well-known/acme-challenge/$token" -o "$work/probe" 2>/dev/null) || fail network
    python3 - "$ip" <<'PY' >/dev/null 2>&1 || fail
import ipaddress, sys
assert ipaddress.ip_address(sys.argv[1]).is_global
PY
    [[ $(cat "$work/probe") == "$token" ]] || fail challenge
    rm -f -- "$probe"; probe=
    acme --issue -d "$DOMAIN" -w "$WEBROOT" --keylength 2048 || fail
    # Avoid acme.sh's manual-install backup of an earlier staged private key.
    rm -f -- "$STAGE/fullchain.pem" "$STAGE/privkey.pem"
    acme --install-cert -d "$DOMAIN" --key-file "$STAGE/privkey.pem" --fullchain-file "$STAGE/fullchain.pem" --reloadcmd '' || fail
    domain_policy >/dev/null 2>&1 || fail state-policy
    validate_pair "$STAGE/fullchain.pem" "$STAGE/privkey.pem" 1 || fail
    printf 'CERT_BOOTSTRAP=READY_FOR_ADOPTION\n';;
  --adopt)
    [[ ! -e $ACME_HOME/.renew-critical && -f $ACME_HOME/certs/$DOMAIN/$DOMAIN.conf ]] || fail
    domain_policy >/dev/null 2>&1 || fail state-policy
    validate_pair "$STAGE/fullchain.pem" "$STAGE/privkey.pem" 1 || fail
    [[ ! -e $MARKER ]] || fail
    promote;;
  --run)
    [[ -f $MARKER && $(stat -c %u "$MARKER") == 0 && $(stat -c %a "$MARKER") == 600 ]] || fail
    [[ -f $ACME_HOME/certs/$DOMAIN/$DOMAIN.conf ]] || fail state-policy
    domain_policy >/dev/null 2>&1 || fail state-policy
    acme --cron || fail
    promote;;
esac
