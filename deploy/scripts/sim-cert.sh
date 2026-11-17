#!/usr/bin/env bash
# Issue a client certificate for ONE registered SIMULATION / TEST runtime identity, signed by the
# existing Crane MQTT CA — on the host, with openssl only (no Go toolchain on the server).
#
#   deploy/scripts/sim-cert.sh issue  --ca-dir <dir with ca.crt + ca.key> --out <sim cert dir> --id SIM-XXXX [--days 30]
#   deploy/scripts/sim-cert.sh verify --ca-dir <dir> --out <sim cert dir> --id SIM-XXXX
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
ca_dir="" out="" id="" days=30
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ca-dir) ca_dir="$2"; shift 2;;
    --out) out="$2"; shift 2;;
    --id) id="$2"; shift 2;;
    --days) days="$2"; shift 2;;
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

if [[ "$op" == verify ]]; then
  openssl verify -CAfile "$ca_dir/ca.crt" "$out/$id.crt" >/dev/null || die "certificate does not chain to the CA"
  cn="$(openssl x509 -in "$out/$id.crt" -noout -subject -nameopt multiline | awk -F' = ' '/commonName/{print $2}')"
  [[ "$cn" == "$id" ]] || die "certificate CN is not $id"
  openssl x509 -in "$out/$id.crt" -noout -checkend 86400 >/dev/null || die "certificate expires within 24h"
  [[ "$(stat -c %a "$out/$id.key")" == 600 ]] || die "$id.key must be 0600"
  [[ ! -e "$out/ca.key" ]] || die "CA private key present in the simulator directory"
  echo "SIM_CERT_VERIFY=PASS id=$id"
  exit 0
fi

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
openssl verify -CAfile "$ca_dir/ca.crt" "$work/$id.crt" >/dev/null || die "issued certificate failed verification"
install -m 0644 "$ca_dir/ca.crt" "$out/ca.crt"
install -m 0644 "$work/$id.crt" "$out/$id.crt"
install -m 0600 "$work/$id.key" "$out/$id.key"
echo "SIM_CERT_ISSUE=PASS id=$id days=$days out=$out_real (CA key not copied)"
