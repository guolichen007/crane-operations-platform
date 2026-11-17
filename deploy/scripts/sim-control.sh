#!/usr/bin/env bash
# Control the crane-dev simulator (SIMULATION identities only) next to the running Crane stack.
# Uses the existing opt-in overlay deploy/compose/docker-compose.prod.simulation.yml and a LOCAL
# simulator image built by local-dev-deploy.mjs (crane-local/device-simulator:<sha>), never pulled.
#
#   deploy/scripts/sim-control.sh start  <private.env> --fleet <sim-run-fleet.json> --certs <sim cert dir> [--scenario normal|intermittent|alarm-storm|offline]
#   deploy/scripts/sim-control.sh stop   <private.env>
#   deploy/scripts/sim-control.sh status <private.env>
#
# Checks before start: the run Fleet holds only SIMULATION identities with SIM- ids (the output of
# `runtime-identity merge --simulator`: this request's identities only, never the full SIM subset)
# and each of them is registered identically in the ACTIVE cloud Fleet (CRANE_FLEET_FILE); every id
# has a client certificate in --certs that chains to its ca.crt, with a matching key and CN = id;
# --certs contains no CA private key; container user 10001 can read the Fleet and certificates and
# write the spool volume; the image is the local simulator for the current clean checkout's SHA.
# Compose always includes the ACTIVE local override (local-dev-deploy active-override.yml) so no
# service definition falls back to registry images. Only the device-simulator service is created /
# removed (--no-deps); cloud-api, broker, databases and other platforms are not touched.
set -Eeuo pipefail
umask 077
die() { echo "sim-control: $*" >&2; exit 1; }
root="$(cd "$(dirname "$0")/../.." && pwd)"
op="${1:-}"; env_file="${2:-}"; shift 2 || die "usage: sim-control.sh start|stop|status <private.env> [...]"
fleet="" certs="" scenario=normal
while [[ $# -gt 0 ]]; do
  case "$1" in
    --fleet) fleet="$2"; shift 2;;
    --certs) certs="$2"; shift 2;;
    --scenario) scenario="$2"; shift 2;;
    *) die "unknown argument $1";;
  esac
done
[[ -f "$env_file" && "$(stat -c %a "$env_file")" == 600 ]] || die "private env file must exist with mode 0600"
envval() { sed -n "s/^$1=//p" "$env_file" | tail -1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }
sha="$(git -C "$root" rev-parse HEAD)"
[[ -z "$(git -C "$root" status --porcelain --untracked-files=normal)" ]] || die "clean Git worktree required"
image="crane-local/device-simulator:$sha"
state_dir="$(envval CRANE_LOCAL_DEPLOY_DIR)"; state_dir="${state_dir:-/srv/u-dao/releases/crane-local}"
active="$state_dir/active-override.yml"
[[ -f "$active" ]] || die "$active missing: deploy with local-dev-deploy.mjs apply first (otherwise compose would fall back to registry images)"
compose=(docker compose --project-name u-dao-crane-ops --project-directory "$root/deploy/compose" --env-file "$env_file"
  -f "$root/deploy/compose/docker-compose.prod.yml" -f "$root/deploy/compose/docker-compose.crane-dev.yml" -f "$active"
  -f "$root/deploy/compose/docker-compose.prod.simulation.yml")
SIM_UID=10001
# Readable by the container user: owned by it, or world-readable (public material only).
readable_by_sim() { local u m; read -r u m < <(stat -c '%u %a' "$1"); [[ "$u" == "$SIM_UID" ]] || (( 8#$m & 8#004 )); }
pubhash() { openssl pkey "$@" -outform DER 2>/dev/null | openssl dgst -sha256 -r | cut -d' ' -f1; }

case "$op" in
  start)
    [[ "$scenario" =~ ^(normal|intermittent|alarm-storm|offline)$ ]] || die "unknown scenario"
    [[ -f "$fleet" && -d "$certs" ]] || die "--fleet and --certs required"
    [[ ! -e "$certs/ca.key" ]] || die "CA private key must not be in the simulator certificate directory"
    docker image inspect "$image" >/dev/null 2>&1 || die "$image not built (run local-dev-deploy.mjs build --services device-simulator)"
    [[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" == "$sha" ]] || die "simulator image revision is not $sha"
    cloud_fleet="$(envval CRANE_FLEET_FILE)"
    [[ -f "$cloud_fleet" ]] || die "CRANE_FLEET_FILE ($cloud_fleet) not found"
    ids="$(python3 -c '
import json, re, sys
m = json.load(open(sys.argv[1]))
cloud = {d["runtimeDeviceId"]: d for d in json.load(open(sys.argv[2])).get("devices", [])}
ids = []
for d in m.get("devices", []):
    if d.get("sourceMode") != "SIMULATION" or not re.fullmatch(r"SIM-[A-Za-z0-9_-]{1,76}", d.get("runtimeDeviceId", "")):
        sys.exit("fleet may contain SIMULATION SIM-* identities only")
    if cloud.get(d["runtimeDeviceId"]) != d:
        sys.exit(d["runtimeDeviceId"] + " is not registered identically in the active cloud Fleet (local-dev-deploy.mjs fleet first)")
    ids.append(d["runtimeDeviceId"])
if not 1 <= len(ids) <= 20:
    sys.exit("run fleet must contain 1..20 identities (use the request-only output of merge --simulator)")
print(" ".join(ids))
' "$fleet" "$cloud_fleet")" || die "invalid simulator fleet"
    readable_by_sim "$fleet" || die "$fleet is not readable by uid $SIM_UID (chown $SIM_UID or chmod 0644; it holds no secret)"
    [[ "$(stat -c '%u %a' "$certs")" == "$SIM_UID 700" ]] || die "$certs must be owned by $SIM_UID with mode 0700 (sim-cert.sh --owner $SIM_UID:$SIM_UID)"
    readable_by_sim "$certs/ca.crt" || die "$certs/ca.crt not readable by uid $SIM_UID"
    for id in $ids; do
      [[ "$(stat -c '%u %a' "$certs/$id.key")" == "$SIM_UID 600" ]] || die "$id.key must be owned by $SIM_UID with mode 0600 (sim-cert.sh --owner $SIM_UID:$SIM_UID)"
      readable_by_sim "$certs/$id.crt" || die "$id.crt not readable by uid $SIM_UID"
      openssl verify -purpose sslclient -CAfile "$certs/ca.crt" "$certs/$id.crt" >/dev/null 2>&1 || die "$id has no client certificate chaining to $certs/ca.crt"
      cn="$(openssl x509 -in "$certs/$id.crt" -noout -subject -nameopt multiline | awk -F' = ' '/commonName/{print $2}')"
      [[ "$cn" == "$id" ]] || die "$id.crt CN is $cn"
      a="$(openssl x509 -in "$certs/$id.crt" -noout -pubkey | pubhash -pubin)"; b="$(pubhash -in "$certs/$id.key" -pubout)"
      [[ -n "$a" && "$a" == "$b" ]] || die "$id.key does not match $id.crt"
    done
    # The spool volume must be writable by the simulator user (a volume created root-owned is not).
    vol=u-dao-crane-ops_crane-simulation-spool
    if docker volume inspect "$vol" >/dev/null 2>&1; then
      owner="$(docker run --rm --pull never --network none --entrypoint stat -v "$vol:/spool" "$image" -c %u /spool)" || die "cannot inspect $vol"
      [[ "$owner" == "$SIM_UID" ]] || die "$vol is owned by uid $owner, not $SIM_UID (with the simulator stopped: docker volume rm $vol)"
    fi
    CRANE_SIMULATOR_IMAGE="$image" CRANE_FLEET_FILE="$(realpath "$fleet")" CRANE_SIMULATOR_CERT_DIR="$(realpath "$certs")" \
      "${compose[@]}" --profile simulation run -d --no-deps --pull never --name u-dao-crane-ops-sim --rm device-simulator \
      --fleet-file /inventory/fleet.json --scenario "$scenario" >/dev/null
    sleep 5
    docker ps --filter name=^u-dao-crane-ops-sim$ --format '{{.Status}}' | grep -q '^Up' || die "simulator exited right after start (docker logs are removed with --rm; rerun with the same inputs and inspect)"
    echo "SIM_START=PASS image=$image scenario=$scenario devices=$(wc -w <<<"$ids") ids=$(tr ' ' ',' <<<"$ids")"
    ;;
  stop)
    if docker ps -q --filter name=^u-dao-crane-ops-sim$ | grep -q .; then docker stop --time 10 u-dao-crane-ops-sim >/dev/null; fi
    echo "SIM_STOP=PASS (graceful DEATH sent by the Agent on SIGTERM)"
    ;;
  status)
    docker ps --filter name=^u-dao-crane-ops-sim$ --format '{{.Names}} {{.Image}} {{.Status}}'
    ;;
  *) die "usage: sim-control.sh start|stop|status <private.env> [...]";;
esac
