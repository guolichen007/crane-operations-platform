#!/usr/bin/env bash
# Issue a client certificate for ONE registered SIMULATION / TEST runtime identity, signed by the
# existing Crane MQTT CA — on the host, with openssl only (no Go toolchain on the server).
#
#   deploy/scripts/sim-cert.sh issue  --ca-dir <dir with ca.crt + ca.key> --out <sim cert dir> --id SIM-XXXX [--days 30] [--owner 10001:10001]
#   deploy/scripts/sim-cert.sh verify --ca-dir <dir> --out <sim cert dir> --id SIM-XXXX [--owner 10001:10001]
#
# The CA may be RSA or EC (the existing site CA is used as is; it is never re-created). Checks:
# ca.crt is a CA certificate and ca.key is ITS key; the CA stays valid beyond the leaf's notAfter;
# the issued leaf chains to ca.crt, its key matches, CN = id, extendedKeyUsage = clientAuth.
# --owner (root only) hands the output directory and the leaf key to the simulator container user
# (uid 10001 in docker-compose.prod.simulation.yml): directory 0700 and key 0600 owned by that uid,
# certificates 0644 — readable inside the container, private to it on the host.
#
# Rules:
#   - only ids starting with SIM- or TEST- (REAL / EDGE- identities are never issued here);
#   - the CA private key is read from the host secret directory only; it is never copied into the
#     output directory, which is the ONLY directory mounted into the simulator container;
#   - the output directory holds ca.crt (public) + <id>.crt + <id>.key (0600);
#   - an existing certificate for the id is never overwritten (revoke/remove it explicitly first);
#   - the subject CN is exactly the id: the broker maps it to the MQTT username
#     (use_identity_as_username) and the generated ACL scopes it to that one device's topics.
set -Eeuo pipefail
umask 077
die() { echo "sim-cert: $*" >&2; exit 1; }
op="${1:-}"; shift || true
ca_dir="" out="" id="" days=30 owner=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ca-dir) ca_dir="$2"; shift 2;;
    --out) out="$2"; shift 2;;
    --id) id="$2"; shift 2;;
    --days) days="$2"; shift 2;;
    --owner) owner="$2"; shift 2;;
    *) die "unknown argument $1";;
  esac
done
[[ "$op" == issue || "$op" == verify ]] || die "usage: sim-cert.sh issue|verify --ca-dir DIR --out DIR --id SIM-XXXX [--days N]"
[[ "$id" =~ ^(SIM|TEST)-[A-Za-z0-9_-]{1,75}$ ]] || die "id must be SIM-* or TEST-* (REAL identities are not issued by this tool)"
[[ "$days" =~ ^[0-9]+$ && "$days" -ge 1 && "$days" -le 90 ]] || die "--days must be 1..90"
[[ -f "$ca_dir/ca.crt" && -f "$ca_dir/ca.key" ]] || die "CA directory must contain ca.crt and ca.key"
[[ -n "$out" ]] || die "--out required"
ca_real="$(realpath "$ca_dir")"; mkdir -p "$out"; chmod 0700 "$out"; out_real="$(realpath "$out")"
[[ "$out_real" != "$ca_real" && "$out_real" != "$ca_real"/* ]] || die "output directory must not be (inside) the CA directory"
key_mode="$(stat -c %a "$ca_dir/ca.key")"
[[ "$key_mode" == 600 || "$key_mode" == 400 ]] || die "ca.key must be mode 0600/0400"
if [[ -n "$owner" ]]; then
  [[ "$owner" =~ ^[0-9]{1,6}:[0-9]{1,6}$ ]] || die "--owner must be numeric UID:GID"
  [[ "$(id -u)" == 0 ]] || die "--owner requires root (chown)"
fi
pub() { openssl pkey -in "$1" -pubout -outform DER 2>/dev/null | openssl dgst -sha256 -r | cut -d' ' -f1; }
certpub() { openssl x509 -in "$1" -noout -pubkey | openssl pkey -pubin -outform DER 2>/dev/null | openssl dgst -sha256 -r | cut -d' ' -f1; }
openssl x509 -in "$ca_dir/ca.crt" -noout -ext basicConstraints 2>/dev/null | grep -q "CA:TRUE" || die "ca.crt is not a CA certificate"
[[ -n "$(certpub "$ca_dir/ca.crt")" && "$(certpub "$ca_dir/ca.crt")" == "$(pub "$ca_dir/ca.key")" ]] || die "ca.key does not belong to ca.crt"
ca_alg="$(openssl x509 -in "$ca_dir/ca.crt" -noout -text | awk -F': ' '/Public Key Algorithm/{print $2; exit}')"
ca_cn="$(openssl x509 -in "$ca_dir/ca.crt" -noout -subject -nameopt multiline | awk -F' = ' '/commonName/{print $2}')"
[[ "${ca_cn,,}" != *development* ]] || echo "sim-cert: WARNING CA \"$ca_cn\" is named development: cloud-api (APP_ENV=production) refuses such a trust bundle; use the site CA" >&2

check_leaf() {
  openssl verify -purpose sslclient -CAfile "$ca_dir/ca.crt" "$1/$id.crt" >/dev/null 2>&1 || die "certificate does not chain to the CA for client authentication"
  cn="$(openssl x509 -in "$1/$id.crt" -noout -subject -nameopt multiline | awk -F' = ' '/commonName/{print $2}')"
  [[ "$cn" == "$id" ]] || die "certificate CN is not $id"
  openssl x509 -in "$1/$id.crt" -noout -ext extendedKeyUsage 2>/dev/null | grep -q "TLS Web Client Authentication" || die "certificate lacks clientAuth"
  [[ "$(certpub "$1/$id.crt")" == "$(pub "$1/$id.key")" ]] || die "$id.key does not match $id.crt"
  leaf_end="$(date -d "$(openssl x509 -in "$1/$id.crt" -noout -enddate | cut -d= -f2)" +%s)"
  ca_end="$(date -d "$(openssl x509 -in "$ca_dir/ca.crt" -noout -enddate | cut -d= -f2)" +%s)"
  (( ca_end >= leaf_end )) || die "CA expires before the leaf certificate"
}
if [[ "$op" == verify ]]; then
  check_leaf "$out"
  openssl x509 -in "$out/$id.crt" -noout -checkend 86400 >/dev/null || die "certificate expires within 24h"
  [[ "$(stat -c %a "$out/$id.key")" == 600 ]] || die "$id.key must be 0600"
  [[ ! -e "$out/ca.key" ]] || die "CA private key present in the simulator directory"
  cmp -s "$out/ca.crt" "$ca_dir/ca.crt" || die "$out/ca.crt is not the CA certificate"
  if [[ -n "$owner" ]]; then
    [[ "$(stat -c '%u:%g %a' "$out")" == "$owner 700" ]] || die "$out must be owned by $owner with mode 0700"
    [[ "$(stat -c '%u:%g' "$out/$id.key")" == "$owner" ]] || die "$id.key must be owned by $owner"
  fi
  echo "SIM_CERT_VERIFY=PASS id=$id ca=\"$ca_cn\" ca_alg=$ca_alg"
  exit 0
fi
# The CA must outlive the requested leaf.
ca_end="$(date -d "$(openssl x509 -in "$ca_dir/ca.crt" -noout -enddate | cut -d= -f2)" +%s)"
(( ca_end >= $(date +%s) + days * 86400 + 300 )) || die "CA ($ca_cn) expires before a ${days}-day leaf would; choose fewer --days (CA renewal is a separate decision)"

[[ ! -e "$out/$id.crt" && ! -e "$out/$id.key" ]] || die "$id already has a certificate in $out (never overwritten)"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
openssl ecparam -name prime256v1 -genkey -noout -out "$work/key.pem"
openssl pkcs8 -topk8 -nocrypt -in "$work/key.pem" -out "$work/$id.key"
openssl req -new -key "$work/$id.key" -subj "/CN=$id" -out "$work/req.csr"
cat > "$work/ext.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOF
openssl x509 -req -in "$work/req.csr" -CA "$ca_dir/ca.crt" -CAkey "$ca_dir/ca.key" -set_serial "0x$(openssl rand -hex 16)" \
  -days "$days" -sha256 -extfile "$work/ext.cnf" -out "$work/$id.crt" 2>/dev/null
check_leaf "$work"
if [[ -e "$out/ca.crt" ]]; then cmp -s "$out/ca.crt" "$ca_dir/ca.crt" || die "$out/ca.crt differs from the CA certificate (never overwritten)"; else install -m 0644 "$ca_dir/ca.crt" "$out/ca.crt"; fi
install -m 0644 "$work/$id.crt" "$out/$id.crt"
install -m 0600 "$work/$id.key" "$out/$id.key"
if [[ -n "$owner" ]]; then
  chown "$owner" "$out" "$out/$id.key" "$out/$id.crt" "$out/ca.crt"
  chmod 0700 "$out"
fi
echo "SIM_CERT_ISSUE=PASS id=$id days=$days ca=\"$ca_cn\" ca_alg=$ca_alg out=$out_real owner=${owner:-$(id -u):$(id -g)} (CA key not copied)"
