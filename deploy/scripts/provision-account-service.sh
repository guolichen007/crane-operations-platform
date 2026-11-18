#!/usr/bin/env bash
# One-shot, host-administrator run of provision-keycloak for crane-dev: converges the crane realm,
# the crane-bff client (unchanged rules) and the restricted crane-account-service client
# (view/query/manage-users only, scope mapping, admin-only user attributes).
#
#   sudo deploy/scripts/provision-account-service.sh <private.env>
#
# - Uses the LOCAL cloud-api image of the ACTIVE deployment (crane-local/cloud-api:<sha>), never pulled.
# - Runs on the Crane backend network only (http://keycloak:8080); nothing is published.
# - Only this throw-away container reads keycloak_bootstrap_password; cloud-api never mounts it.
# - Idempotent; any extra role later added to the service account is removed again.
# - Prints no credential or upstream response.
set -Eeuo pipefail
umask 077
die() { echo "provision-account-service: $*" >&2; exit 1; }
root="$(cd "$(dirname "$0")/../.." && pwd)"
env_file="${1:-}"
[[ -f "$env_file" && "$(stat -c %a "$env_file")" == 600 ]] || die "private env file (mode 0600) required"
[[ $(id -u) == 0 ]] || die "run as the host administrator (needs read access to private secret files)"
envval() { sed -n "s/^$1=//p" "$env_file" | tail -1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }
secrets="$(envval CRANE_SECRET_DIR)"; secrets="${secrets:-/srv/u-dao/secrets/crane-ops}"
public="$(envval CRANE_PUBLIC_URL)"; public="${public:-https://crane.youdaozd.com}"
for f in keycloak_bootstrap_password oidc_client_secret account_service_secret; do
  [[ -f "$secrets/$f" && ! -L "$secrets/$f" ]] || die "$secrets/$f missing (run deploy/scripts/provision-secrets.sh first)"
done
state_dir="$(envval CRANE_LOCAL_DEPLOY_DIR)"; state_dir="${state_dir:-/srv/u-dao/releases/crane-local}"
image="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["images"]["cloud-api"]["tag"])' "$state_dir/current.json" 2>/dev/null)" \
  || die "$state_dir/current.json missing: deploy with local-dev-deploy.mjs apply first"
docker image inspect "$image" >/dev/null 2>&1 || die "local image $image not present"
network="$(docker network ls --format '{{.Name}}' | grep -Fx u-dao-crane-ops_backend)" || die "Crane backend network not found"
docker run --rm --pull never --network "$network" --user 0:0 --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --entrypoint provision-keycloak \
  -e KEYCLOAK_ADMIN_URL=http://keycloak:8080 -e PUBLIC_URL="$public" \
  -e KEYCLOAK_BOOTSTRAP_PASSWORD_FILE=/s/keycloak_bootstrap_password -e OIDC_CLIENT_SECRET_FILE=/s/oidc_client_secret \
  -e ACCOUNT_SERVICE_SECRET_FILE=/s/account_service_secret -e KEYCLOAK_REALM_TEMPLATE=/t/realm.template.json \
  -v "$secrets/keycloak_bootstrap_password:/s/keycloak_bootstrap_password:ro" \
  -v "$secrets/oidc_client_secret:/s/oidc_client_secret:ro" \
  -v "$secrets/account_service_secret:/s/account_service_secret:ro" \
  -v "$root/deploy/production/realm.template.json:/t/realm.template.json:ro" \
  "$image"
echo "crane-account-service converged; restart cloud-api with: node deploy/scripts/local-dev-deploy.mjs compose $env_file -- up -d --no-deps cloud-api"
