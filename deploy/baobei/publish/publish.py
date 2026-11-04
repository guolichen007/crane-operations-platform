#!/usr/bin/env python3
"""Manual GHCR release only. No deployment, credentials in builds, or local image-ID digests."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request

REPOSITORY = 'guolichen007/crane-operations-platform'
SOURCE = 'https://github.com/' + REPOSITORY
API = 'https://api.github.com/repos/' + REPOSITORY
ENVIRONMENT = 'baobei-ghcr-publish'
RELEASE_PREFIX = 'refs/heads/release/baobei-publish-'
BRANCH = 'product/baobei-v1'
IMAGES = {k: 'ghcr.io/guolichen007/baobei-' + k for k in ('web', 'api')}
GATES = {'baobei-' + k for k in ('contract', 'web-quality', 'api-test', 'db-test', 'e2e', 'security', 'compose', 'build')}
DIGEST = re.compile(r'^sha256:[a-f0-9]{64}$')

class ClosureError(Exception):
    pass

def run(args, *, env=None, stdin=None):
    result = subprocess.run(args, input=stdin, env=env, text=True, capture_output=True, check=False)
    if result.returncode:
        raise ClosureError('Release command failed: ' + args[0])
    return result.stdout

def get_json(url):
    request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'baobei-publish', 'X-GitHub-Api-Version': '2022-11-28'})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)

def verify_ci(sha, fetch=get_json):
    if not re.fullmatch(r'[a-f0-9]{40}', sha):
        raise ClosureError('Full source SHA required')
    base = 'https://api.github.com/repos/' + REPOSITORY
    data = fetch(base + '/actions/workflows/baobei-ci.yml/runs?head_sha=' + sha + '&per_page=100')
    candidates = [r for r in data['workflow_runs'] if r['head_sha'] == sha and r['head_branch'] == BRANCH and r['path'] == '.github/workflows/baobei-ci.yml' and r['event'] in ('push', 'workflow_dispatch')]
    if not candidates:
        raise ClosureError('No matching Baobei CI')
    latest = max(candidates, key=lambda r: (r['id'], r.get('run_attempt', 1)))
    if latest['status'] != 'completed' or latest['conclusion'] != 'success':
        raise ClosureError('Latest Baobei CI must succeed')
    jobs = fetch(base + '/actions/runs/' + str(latest['id']) + '/attempts/' + str(latest.get('run_attempt', 1)) + '/jobs?per_page=100')
    if jobs['total_count'] != 8 or len(jobs['jobs']) != 8 or {j['name'] for j in jobs['jobs']} != GATES or any(j['status'] != 'completed' or j['conclusion'] != 'success' for j in jobs['jobs']):
        raise ClosureError('All eight gates must succeed in the same run attempt')
    return latest['id']

def release_branch(sha, env, event):
    ref = RELEASE_PREFIX + sha
    if (not re.fullmatch(r'[a-f0-9]{40}', sha)
            or env.get('GITHUB_SHA') != sha or env.get('GITHUB_REF') != ref
            or env.get('GITHUB_REPOSITORY') != REPOSITORY or env.get('GITHUB_EVENT_NAME') != 'push'
            or env.get('GITHUB_RUN_ATTEMPT') != '1'
            or not re.fullmatch(r'[0-9]+', env.get('GITHUB_RUN_ID', ''))
            or event.get('ref') != ref or event.get('after') != sha
            or event.get('created') is not True or event.get('forced') is not False
            or event.get('deleted') is not False):
        raise ClosureError('RELEASE_REF_DENIED')
    return ref.removeprefix('refs/heads/')


def verify_environment(branch, fetch, run_id=None, actor_id=None):
    url = API + '/environments/' + ENVIRONMENT
    try:
        settings = fetch(url)
        rules = settings['protection_rules']
        reviewers = [r for r in rules if r['type'] == 'required_reviewers']
        policies = fetch(url + '/deployment-branch-policies?per_page=100')
        if (settings['name'] != ENVIRONMENT or type(settings['id']) is not int
                or len(reviewers) != 1 or not reviewers[0]['reviewers']
                or reviewers[0].get('prevent_self_review') is not True
                or settings['deployment_branch_policy'] != {
                    'protected_branches': False, 'custom_branch_policies': True}
                or policies['total_count'] != 1 or len(policies['branch_policies']) != 1
                or policies['branch_policies'][0]['name'] != branch
                or policies['branch_policies'][0]['type'] != 'branch'):
            raise ValueError('Environment protection mismatch')
        reviewer_ids = {
            item['reviewer']['id'] for item in reviewers[0]['reviewers']
            if item['type'] == 'User' and type(item['reviewer']['id']) is int
        }
        if len(reviewer_ids) != len(reviewers[0]['reviewers']):
            raise ValueError('Direct user reviewers required')
        if run_id is not None:
            reviews = fetch(API + '/actions/runs/' + run_id + '/approvals')
            if not actor_id or not any(
                review['state'] == 'approved'
                and review['user']['id'] in reviewer_ids
                and str(review['user']['id']) != actor_id
                and any(e['id'] == settings['id'] and e['name'] == ENVIRONMENT
                        for e in review['environments'])
                for review in reviews
            ):
                raise ValueError('Human approval missing')
    except Exception as error:
        raise ClosureError('RELEASE_ENV_NOT_VERIFIED') from error


def verify_release(sha, env, event, execute, fetch, *, require_approval):
    branch = release_branch(sha, env, event)
    if execute(['git', 'rev-parse', 'HEAD']).strip() != sha:
        raise ClosureError('Checkout must match source SHA')
    execute(['git', 'merge-base', '--is-ancestor', sha, 'origin/' + BRANCH])
    if execute(['git', 'status', '--porcelain', '--untracked-files=normal']).strip():
        raise ClosureError('Clean checkout required')
    ci_run = verify_ci(sha, fetch)
    verify_environment(branch, fetch, env['GITHUB_RUN_ID'] if require_approval else None,
                       env.get('GITHUB_ACTOR_ID'))
    return ci_run


def registry_digest(output):
    matches = re.findall(r'^Digest:\s+(sha256:[a-f0-9]{64})\s*$', output, re.MULTILINE)
    if len(matches) != 1:
        raise ClosureError('Registry manifest digest missing or ambiguous')
    return matches[0]

def verify_image(items, ref, sha):
    if not any(ref.startswith(name + '@') for name in IMAGES.values()) or not DIGEST.fullmatch(ref.rsplit('@', 1)[-1]):
        raise ClosureError('Fixed GHCR repository and registry digest required')
    if len(items) != 1:
        raise ClosureError('Expected one pulled image')
    image = items[0]
    labels = image.get('Config', {}).get('Labels', {})
    if labels.get('org.opencontainers.image.revision') != sha or labels.get('org.opencontainers.image.source') != SOURCE or ref not in image.get('RepoDigests', []):
        raise ClosureError('Pulled revision/source/RepoDigests mismatch')

def atomic_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix='.manifest-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)

def publish(root, sha, env, event, token, actor, execute=run, fetch=get_json):
    release_branch(sha, env, event)
    run_id = env['GITHUB_RUN_ID'] + '-' + env['GITHUB_RUN_ATTEMPT']
    root = Path(root)
    out = root / 'local-private/baobei/registry-release' / sha
    out.mkdir(parents=True, exist_ok=True)
    manifest_path = out / 'deployment-manifest.json'
    manifest_path.unlink(missing_ok=True)
    atomic_json(out / 'publish-status.json', {'READY_FOR_PRODUCTION': 'NO', 'S1B_PUBLIC_DEPLOY': 'NOT_AUTHORIZED', 'registry_published': False})
    ci_run = verify_release(sha, env, event, execute, fetch, require_approval=True)
    # No credential can reach Docker build args/context or build subprocess environment.
    clean_env = {k: v for k, v in os.environ.items() if k not in ('GITHUB_TOKEN', 'GH_TOKEN', 'NODE_AUTH_TOKEN', 'DOCKER_AUTH_CONFIG')}
    tags = {kind: name + ':sha-' + sha + '-run-' + run_id for kind, name in IMAGES.items()}
    for kind, context in [('web', 'apps/project-intake-web'), ('api', 'services/project-intake-api')]:
        args = ['docker', 'build', '--platform', 'linux/amd64', '--file', 'deploy/baobei/' + kind + '.Dockerfile', '--build-arg', 'SOURCE_SHA=' + sha,
                '--label', 'org.opencontainers.image.revision=' + sha, '--label', 'org.opencontainers.image.source=' + SOURCE, '--tag', tags[kind]]
        if kind == 'web':
            args += ['--build-context', 'baobei_config=deploy/baobei']
        execute(args + [context], env=clean_env)
    refs = {}
    # Ephemeral Docker credentials; no ambient runner auth or persisted checkout token.
    with tempfile.TemporaryDirectory(prefix='baobei-ghcr-') as auth, tempfile.TemporaryDirectory(prefix='baobei-public-pull-') as anonymous:
        auth_env = {**clean_env, 'DOCKER_CONFIG': auth}
        public_env = {**clean_env, 'DOCKER_CONFIG': anonymous}
        if not token or not re.fullmatch(r'[A-Za-z0-9-]+', actor):
            raise ClosureError('Registry workflow identity required')
        try:
            execute(['docker', 'login', 'ghcr.io', '--username', actor, '--password-stdin'], env=auth_env, stdin=token + '\n')
            for kind, name in IMAGES.items():
                execute(['docker', 'push', tags[kind]], env=auth_env)
                digest = registry_digest(execute(['docker', 'buildx', 'imagetools', 'inspect', tags[kind]], env=auth_env))
                ref = name + '@' + digest
                execute(['docker', 'pull', '--platform', 'linux/amd64', ref], env=auth_env)
                verify_image(json.loads(execute(['docker', 'image', 'inspect', ref], env=auth_env)), ref, sha)
                refs[kind] = (ref, digest)
            # Verify public access after both packages have been pushed. A first
            # release can create both private packages for the owner to review.
            for ref, _ in refs.values():
                try:
                    execute(['docker', 'pull', '--platform', 'linux/amd64', ref], env=public_env)
                except ClosureError as error:
                    raise ClosureError('PUBLIC_PULL_NOT_VERIFIED') from error
                verify_image(json.loads(execute(['docker', 'image', 'inspect', ref], env=public_env)), ref, sha)
        finally:
            try:
                execute(['docker', 'logout', 'ghcr.io'], env=auth_env)
            except Exception:
                pass
    sha256 = lambda path: hashlib.sha256((root / path).read_bytes()).hexdigest()
    versions = sorted((root / 'services/project-intake-api/migrations').glob('*.sql'))
    if not versions:
        raise ClosureError('Schema inventory required')
    manifest = {'manifest_kind': 'registry_deployment', 'source_sha': sha,
                'web_registry_ref': refs['web'][0], 'web_registry_digest': refs['web'][1],
                'api_registry_ref': refs['api'][0], 'api_registry_digest': refs['api'][1],
                'openapi_hash': sha256('contracts/project-intake/openapi.yaml'),
                'transfer_hash': sha256('contracts/project-intake/ProjectIntakeTransferV1.schema.json'),
                'schema_version': versions[-1].name.split('_', 1)[0],
                'created_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'registry_published': True, 'public_pull_verified': True, 'ci_run_id': ci_run,
                'platform': 'linux/amd64', 'READY_FOR_PRODUCTION': 'NO', 'S1B_PUBLIC_DEPLOY': 'NOT_AUTHORIZED'}
    atomic_json(manifest_path, manifest)
    atomic_json(out / 'publish-status.json', {'READY_FOR_PRODUCTION': 'NO', 'S1B_PUBLIC_DEPLOY': 'NOT_AUTHORIZED', 'registry_published': True})
    return manifest

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--preflight', action='store_true')
    args = parser.parse_args()
    try:
        event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text(encoding='utf-8'))
        if args.preflight:
            verify_release(args.source_sha, os.environ, event, run, get_json, require_approval=False)
            print('RELEASE_PREFLIGHT=PASS')
            return
        publish(Path.cwd(), args.source_sha, os.environ, event,
                os.environ.get('GITHUB_TOKEN', ''), os.environ['GITHUB_ACTOR'])
        print('REGISTRY_MANIFEST_VERIFIED=YES READY_FOR_PRODUCTION=NO S1B_PUBLIC_DEPLOY=NOT_AUTHORIZED')
    except Exception as error:
        if isinstance(error, ClosureError) and str(error) in {
            'RELEASE_ENV_NOT_VERIFIED', 'PUBLIC_PULL_NOT_VERIFIED',
        }:
            raise SystemExit(str(error) + ' STOP') from None
        raise SystemExit('GHCR release rejected. READY_FOR_PRODUCTION=NO') from None


if __name__ == '__main__':
    main()
