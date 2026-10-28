#!/usr/bin/env bash
set -euo pipefail
umask 077
target=${1:-/srv/u-dao/secrets/crane-ops}
[[ $target = /* && $target != / && ! -L $target ]] || { echo 'Explicit private secret directory required' >&2; exit 1; }
[[ $(id -u) = 0 ]] || { echo 'Provisioning requires the host administrator to set service-specific ownership' >&2; exit 1; }
mkdir -p -- "$target"
chmod 700 -- "$target"
for name in postgres_admin_password app_db_password keycloak_db_password keycloak_bootstrap_password oidc_client_secret session_server_secret; do
  file="$target/$name"
  [[ ! -L $file ]] || { echo 'Secret symlink rejected' >&2; exit 1; }
  if [[ ! -e $file ]]; then openssl rand -hex 32 >"$file"; fi
  chmod 600 -- "$file"
  owner=0
  case $name in app_db_password|oidc_client_secret|session_server_secret) owner=10001;; keycloak_db_password|keycloak_bootstrap_password) owner=1000;; esac
  chown "$owner:$owner" -- "$file"
done
echo 'Private secret files provisioned; existing values preserved and contents never printed.'
echo 'MQTT trust/leaf material and private Inventory require separate reviewed provisioning; no development CA was generated.'
