"""Disposable Keycloak 26.8.1 FGAP V2 probe. No Baobei service uses this credential."""
import json
import os
import re
import secrets
import subprocess
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

IMAGE = "quay.io/keycloak/keycloak:26.8.1"
REALM = "baobei-fgap-fixture"
ROLES = ("baobei_sales", "baobei_manager", "baobei_director", "baobei_admin")
OPENER = build_opener(ProxyHandler({}))


def docker(*args, env=None):
    return subprocess.check_output(DOCKER + list(args), text=True, env=env).strip()


def call(method, path, body=None, token=None, want=200, form=False):
    headers = {}
    data = None
    if token:
        headers["Authorization"] = "Bearer " + token
    if body is not None:
        headers["Content-Type"] = "application/x-www-form-urlencoded" if form else "application/json"
        data = (urlencode(body) if form else json.dumps(body)).encode()
    request = Request(BASE + path, data, headers, method=method)
    try:
        with OPENER.open(request, timeout=5) as response:
            status, payload, location = response.status, response.read(), response.headers.get("Location", "")
    except HTTPError as err:
        status, payload, location = err.code, b"", ""
    if status != want:
        raise AssertionError(f"{method} {path.split('?')[0]}: {status}, expected {want}")
    if location:
        return location.rsplit("/", 1)[-1]
    return json.loads(payload) if payload else None


def token(realm, values):
    return call("POST", f"/realms/{realm}/protocol/openid-connect/token", values, form=True)["access_token"]


def verify():
    for _ in range(60):
        try:
            call("GET", "/realms/master/.well-known/openid-configuration")
            break
        except (OSError, AssertionError):
            time.sleep(1)
    else:
        raise AssertionError("isolated Keycloak did not start")

    admin = token("master", {"grant_type": "password", "client_id": "admin-cli",
                            "username": "fixture-bootstrap", "password": PASSWORD})
    assert call("GET", "/admin/serverinfo", token=admin)["systemInfo"]["version"] == "26.8.1"
    call("POST", "/admin/realms", {"realm": REALM, "enabled": True}, admin, 201)
    root = "/admin/realms/" + REALM
    call("PUT", root, {"adminPermissionsEnabled": True}, admin, 204)
    allowed = call("POST", root + "/users", {"username": "allowed", "enabled": True}, admin, 201)
    outside = call("POST", root + "/users", {"username": "outside", "enabled": True}, admin, 201)

    def client(name, service=False):
        return call("POST", root + "/clients", {"clientId": name, "protocol": "openid-connect",
                    "enabled": True, "publicClient": False, "serviceAccountsEnabled": service,
                    "standardFlowEnabled": False, "directAccessGrantsEnabled": False}, admin, 201)

    def role(client_id, name):
        path = root + f"/clients/{client_id}/roles"
        call("POST", path, {"name": name}, admin, 201)
        return call("GET", path + "/" + name, token=admin)

    executor = client("fixture-executor", True)
    executor_user = call("GET", root + f"/clients/{executor}/service-account-user", token=admin)["id"]
    executor_secret = call("GET", root + f"/clients/{executor}/client-secret", token=admin)["value"]
    baobei, crane, firebot = (client(name) for name in ("baobei-web", "crane-fixture", "firebot-fixture"))
    role_reps = [role(baobei, name) for name in ROLES]
    extra = role(baobei, "unapproved-extra")
    crane_role, firebot_role = role(crane, "operator"), role(firebot, "operator")
    realm_management = call("GET", root + "/clients?clientId=realm-management", token=admin)[0]["id"]
    realm_admin = call("GET", root + f"/clients/{realm_management}/roles/realm-admin", token=admin)
    call("POST", root + "/roles", {"name": "fixture-realm-role"}, admin, 201)
    realm_role = call("GET", root + "/roles/fixture-realm-role", token=admin)
    group = call("POST", root + "/groups", {"name": "fixture-group"}, admin, 201)

    permissions_client = call("GET", root + "/clients?clientId=admin-permissions", token=admin)[0]["id"]
    authz = root + f"/clients/{permissions_client}/authz/resource-server"
    call("POST", authz + "/policy/user", {"name": "only-executor", "logic": "POSITIVE",
         "users": [executor_user]}, admin, 201)
    for resource, scopes, ids in [("Users", ["view", "map-roles"], [allowed]),
                                  ("Roles", ["map-role"], [r["id"] for r in role_reps])]:
        call("POST", authz + "/permission/scope", {"name": "only-" + resource,
             "resourceType": resource, "resources": ids, "scopes": scopes,
             "policies": ["only-executor"]}, admin, 201)

    limited = token(REALM, {"grant_type": "client_credentials", "client_id": "fixture-executor",
                            "client_secret": executor_secret})
    def mapping(user, cid):
        return root + f"/users/{user}/role-mappings/clients/{cid}"

    target = mapping(allowed, baobei)
    for rep in role_reps:
        call("POST", target, [rep], limited, 204)
        call("DELETE", target, [rep], limited, 204)
    # Keycloak's scopes do not enforce the application's single-role invariant.
    call("POST", target, role_reps[:2], limited, 204)
    assigned = call("GET", target, token=admin)
    assert {r["name"] for r in assigned} == set(ROLES[:2])
    call("DELETE", target, role_reps[:2], limited, 204)

    forbidden = [
        ("POST", mapping(outside, baobei), [role_reps[0]]),
        ("POST", target, [extra]),
        ("POST", mapping(allowed, crane), [crane_role]),
        ("POST", mapping(allowed, firebot), [firebot_role]),
        ("POST", mapping(allowed, realm_management), [realm_admin]),
        ("POST", root + f"/users/{allowed}/role-mappings/realm", [realm_role]),
        ("PUT", root + f"/users/{allowed}", {"enabled": False}),
        ("POST", root + "/users", {"username": "forbidden-new-user"}),
        ("PUT", root + f"/users/{allowed}/reset-password", {"type": "password", "value": secrets.token_hex(16)}),
        ("PUT", root + f"/users/{allowed}/groups/{group}", None),
        ("PUT", root + f"/clients/{baobei}", {"enabled": False}),
        ("POST", root + f"/roles-by-id/{role_reps[0]['id']}/composites", [realm_admin]),
        ("PUT", root, {"displayName": "forbidden"}),
    ]
    # Verify removal is also denied for an out-of-scope mapping known to exist.
    call("POST", mapping(outside, baobei), [role_reps[0]], admin, 204)
    call("POST", mapping(allowed, crane), [crane_role], admin, 204)
    forbidden.extend([
        ("DELETE", mapping(outside, baobei), [role_reps[0]]),
        ("DELETE", mapping(allowed, crane), [crane_role]),
    ])
    for method, path, body in forbidden:
        call(method, path, body, limited, 403)

    business_admin = client("fixture-business-admin", True)
    business_subject = call("GET", root + f"/clients/{business_admin}/service-account-user", token=admin)["id"]
    business_secret = call("GET", root + f"/clients/{business_admin}/client-secret", token=admin)["value"]
    app_admin_role = next(rep for rep in role_reps if rep["name"] == "baobei_admin")
    call("POST", mapping(business_subject, baobei), [app_admin_role], admin, 204)
    business_token = token(REALM, {"grant_type": "client_credentials", "client_id": "fixture-business-admin",
                                  "client_secret": business_secret})
    call("POST", target, [role_reps[1]], business_token, 403)
    call("POST", mapping(allowed, realm_management), [realm_admin], business_token, 403)
    print(f"KEYCLOAK_FGAP_FIXTURE=PASS version=26.8.1 denied={len(forbidden) + 2} dual_role_mapping=ALLOWED_BY_KEYCLOAK")


if __name__ == "__main__":
    context = os.environ.get("BAOBEI_DOCKER_CONTEXT", "default")
    DOCKER = ["docker", "--context", context]
    endpoint = json.loads(docker("context", "inspect", context))[0]["Endpoints"]["docker"]["Host"]
    if not endpoint.startswith(("unix://", "npipe://")):
        raise SystemExit("The fixture requires a local Docker Engine.")
    name = "baobei-fgap-" + secrets.token_hex(5)
    PASSWORD = secrets.token_hex(24)
    container = docker("run", "--rm", "--pull", "never", "-d", "--name", name, "-p", "127.0.0.1::8080",
                       "-e", "KC_BOOTSTRAP_ADMIN_USERNAME=fixture-bootstrap",
                       "-e", "KC_BOOTSTRAP_ADMIN_PASSWORD",
                       IMAGE, "start-dev", "--features=admin-fine-grained-authz:v2",
                       env={**os.environ, "KC_BOOTSTRAP_ADMIN_PASSWORD": PASSWORD})
    try:
        binding = docker("port", container, "8080/tcp")
        if not re.fullmatch(r"127\.0\.0\.1:\d+", binding):
            raise AssertionError("expected a loopback-only fixture")
        BASE = "http://" + binding
        verify()
    finally:
        docker("stop", container)
