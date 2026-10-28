#!/bin/bash
set -euo pipefail
KC_DB_PASSWORD=$(</run/secrets/keycloak_db_password)
KC_BOOTSTRAP_ADMIN_PASSWORD=$(</run/secrets/keycloak_bootstrap_password)
[[ ${#KC_DB_PASSWORD} -ge 32 && ${#KC_BOOTSTRAP_ADMIN_PASSWORD} -ge 32 ]] || { echo 'Private Keycloak credential file rejected' >&2; exit 1; }
export KC_DB_PASSWORD KC_BOOTSTRAP_ADMIN_PASSWORD
exec /opt/keycloak/bin/kc.sh start
