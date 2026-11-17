#!/usr/bin/env bash
# Control the crane-dev simulator (SIMULATION identities only) next to the running Crane stack.
# Uses the existing opt-in overlay deploy/compose/docker-compose.prod.simulation.yml and a LOCAL
# simulator image built by local-dev-deploy.mjs (crane-local/device-simulator:<sha>), never pulled.
#
#   deploy/scripts/sim-control.sh start  <private.env> --fleet <sim-fleet.json> --certs <sim cert dir> [--scenario normal|intermittent|alarm-storm|offline]
#   deploy/scripts/sim-control.sh stop   <private.env>
#   deploy/scripts/sim-control.sh status <private.env>
#
# Checks before start: the Fleet holds only SIMULATION identities with SIM- ids; every id has a
# certificate in --certs that chains to its ca.crt; --certs contains no CA private key; the image is
# the local simulator for the current clean checkout's SHA. Only the device-simulator service is
# created / removed (--no-deps); cloud-api, broker, databases and other platforms are not touched.
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
sha="$(git -C "$root" rev-parse HEAD)"
[[ -z "$(git -C "$root" status --porcelain --untracked-files=normal)" ]] || die "clean Git worktree required"
image="crane-local/device-simulator:$sha"
compose=(docker compose --project-name u-dao-crane-ops --project-directory "$root/deploy/compose" --env-file "$env_file"
  -f "$root/deploy/compose/docker-compose.prod.yml" -f "$root/deploy/compose/docker-compose.crane-dev.yml"
  -f "$root/deploy/compose/docker-compose.prod.simulation.yml")

case "$op" in
  start)
    [[ "$scenario" =~ ^(normal|intermittent|alarm-storm|offline)$ ]] || die "unknown scenario"
    [[ -f "$fleet" && -d "$certs" ]] || die "--fleet and --certs required"
    [[ ! -e "$certs/ca.key" ]] || die "CA private key must not be in the simulator certificate directory"
    docker image inspect "$image" >/dev/null 2>&1 || die "$image not built (run local-dev-deploy.mjs build --services device-simulator)"
    [[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" == "$sha" ]] || die "simulator image revision is not $sha"
    ids="$(python3 - "$fleet" <<'PY'
import json, re, sys
m = json.load(open(sys.argv[1]))
ids = []
for d in m.get("devices", []):
    if d.get("sourceMode") != "SIMULATION" or not re.fullmatch(r"SIM-[A-Za-z0-9_-]{1,76}", d.get("runtimeDeviceId", "")):
        sys.exit("fleet may contain SIMULATION SIM-* identities only")
    ids.append(d["runtimeDeviceId"])
if not 1 <= len(ids) <= 100: sys.exit("fleet must contain 1..100 identities")
print(" ".join(ids))
PY
)" || die "invalid simulator fleet"
    # The simulator runs as uid 10001 (overlay): its keys must be readable by that uid only.
    for f in "$certs"/*.key; do
      [[ "$(stat -c '%u %a' "$f")" == "10001 600" ]] || die "$f must be owned by 10001 with mode 0600 (sudo chown 10001:10001 <certs>/*.key)"
    done
    for id in $ids; do
      openssl verify -CAfile "$certs/ca.crt" "$certs/$id.crt" >/dev/null 2>&1 || die "$id has no certificate chaining to $certs/ca.crt"
    done
    CRANE_SIMULATOR_IMAGE="$image" CRANE_FLEET_FILE="$(realpath "$fleet")" CRANE_SIMULATOR_CERT_DIR="$(realpath "$certs")" \
      "${compose[@]}" --profile simulation run -d --no-deps --pull never --name u-dao-crane-ops-sim --rm device-simulator \
      --fleet-file /inventory/fleet.json --scenario "$scenario" >/dev/null
    echo "SIM_START=PASS image=$image scenario=$scenario devices=$(wc -w <<<"$ids")"
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
