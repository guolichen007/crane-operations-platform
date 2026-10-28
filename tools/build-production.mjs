// Portable local verification build. Canonical CI release builds the tracked multi-stage Dockerfiles.
import { spawnSync } from 'node:child_process';
import { mkdirSync, cpSync, writeFileSync, existsSync, openSync, closeSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
const docker = process.platform === 'win32' ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const directory = resolve(`tools/.cache/production-build-${process.pid}`); mkdirSync(directory, { recursive: true });
const env = { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0', GOCACHE: resolve('tools/.cache/gobuild'), GOMODCACHE: resolve('tools/.cache/gomod'), GOPATH: resolve('tools/.cache/gopath') };
function run(command, args, options = {}) { const result = spawnSync(command, args, { stdio: 'inherit', env, ...options }); if (result.error || result.status) throw Error('Production local build failed: ' + command); }
for (const [name, path] of [['service', './services/cloud-api/cmd/cloud-api'], ['identity', './tools/identity'], ['provision-keycloak', './tools/provision-keycloak']]) run(go, ['build', '-trimpath', '-ldflags=-s -w', '-o', join(directory, name), path]);
const npm = process.platform === 'win32' ? [process.execPath, [join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), 'run', 'build']] : ['npm', ['run', 'build']];
run(npm[0], npm[1], { env: { ...process.env, VITE_DATA_MODE: 'api', VITE_AUTH_MODE: 'oidc' } });
cpSync('apps/web-console/dist', join(directory, 'web'), { recursive: true });
cpSync('deploy/nginx/production.conf', join(directory, 'nginx.conf'));
cpSync('licenses', join(directory, 'licenses'), { recursive: true });
cpSync('THIRD_PARTY_NOTICES.md', join(directory, 'THIRD_PARTY_NOTICES.md'));
writeFileSync(join(directory, 'Dockerfile.cloud'), 'FROM alpine:3.23\nRUN apk add --no-cache ca-certificates && addgroup -g 10001 app && adduser -D -u 10001 -G app app\nCOPY --chmod=0755 service identity provision-keycloak /usr/local/bin/\nCOPY licenses /usr/share/licenses/crane/\nCOPY THIRD_PARTY_NOTICES.md /usr/share/licenses/crane/THIRD_PARTY_NOTICES.md\nLABEL org.opencontainers.image.revision="local-working-tree"\nUSER 10001:10001\nENTRYPOINT ["service"]\n');
writeFileSync(join(directory, 'Dockerfile.web'), 'FROM nginx:1.28-alpine\nCOPY nginx.conf /etc/nginx/nginx.conf\nCOPY web /usr/share/nginx/html\nCOPY licenses /usr/share/licenses/crane/\nCOPY THIRD_PARTY_NOTICES.md /usr/share/licenses/crane/THIRD_PARTY_NOTICES.md\nLABEL org.opencontainers.image.revision="local-working-tree"\nUSER 101:101\nENTRYPOINT ["nginx", "-g", "daemon off;"]\n');
const archive = resolve(`tools/.cache/production-build-${process.pid}.tar`); run('tar', ['-cf', archive, '-C', directory, '.']);
for (const name of ['cloud', 'web']) { const fd = openSync(archive, 'r'); try { run(docker, ['build', '-f', `Dockerfile.${name}`, '-t', `crane-a1-${name}:local`, '-'], { stdio: [fd, 'inherit', 'inherit'] }); } finally { closeSync(fd); } }
