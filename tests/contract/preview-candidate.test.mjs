import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import { checkInputs, PREVIEW_IMAGES, TAG_PATTERN } from '../../tools/preview-candidate.mjs';

const workflow = () => YAML.parse(readFileSync('.github/workflows/preview-candidate.yml', 'utf8'));
const script = readFileSync('tools/preview-candidate.mjs', 'utf8');
const good = {
  GITHUB_SHA: 'd0f309e7278417570a8b779ea32451fc77cae756', GITHUB_REPOSITORY: 'guolichen007/crane-operations-platform',
  GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'preview/h3-rc1', CI_RUN_ID: '42584696398',
};

test('Preview 候选只由 preview/h3-* 标签触发，不随分支推送或 PR 运行', () => {
  const on = workflow().on;
  assert.deepEqual(Object.keys(on), ['push']);
  assert.deepEqual(on.push.tags, ['preview/h3-*']);
  assert.equal(on.push.branches, undefined);
  // ci.yml 只按分支触发，因此标签推送不会顺带触发 production-release。
  const ci = YAML.parse(readFileSync('.github/workflows/ci.yml', 'utf8'));
  assert.equal(ci.on.push.tags, undefined);
  assert.ok(ci.jobs['production-release'].if.includes("refs/heads/main"));
});

test('Preview 候选不复用生产发布链路、不使用 latest、不部署', () => {
  const text = readFileSync('.github/workflows/preview-candidate.yml', 'utf8');
  for (const forbidden of ['release.mjs', 'production-release', 'task.mjs release', ':latest', 'ssh ', 'docker compose', 'up -d'])
    assert.ok(!text.includes(forbidden), `workflow must not contain ${forbidden}`);
  assert.ok(!script.includes(':latest'));
  assert.ok(!/\/(cloud-api|web-console)[`'"]/.test(script), 'production image names must not be written');
  assert.deepEqual(PREVIEW_IMAGES.map(([n]) => n), ['preview-cloud-api', 'preview-web-console']);
  assert.ok(script.includes("'--exit-code', '1'"), 'Trivy HIGH/CRITICAL gate must fail the run');
  assert.ok(script.includes("labels['org.opencontainers.image.revision'] !== sha"));
});

test('Preview 候选在构建前强制核对同 SHA 九项 CI 全绿', () => {
  const steps = workflow().jobs['preview-candidate'].steps;
  const gate = steps.findIndex((s) => s.id === 'gate');
  const build = steps.findIndex((s) => (s.run || '').includes('tools/preview-candidate.mjs'));
  assert.ok(gate >= 0 && build > gate, 'CI gate must run before the build');
  for (const job of ['a1-security', 'b4-business-acceptance', 'backend', 'edge', 'frontend', 'integration', 'r2-db-test', 'repository-contract', 'security'])
    assert.ok(steps[gate].run.includes(job), `gate must require ${job}`);
  assert.equal(workflow().jobs['preview-candidate'].permissions.packages, 'write');
  assert.equal(workflow().permissions.contents, 'read');
});

test('Preview 候选输入校验 fail-closed', () => {
  assert.deepEqual(checkInputs(good), []);
  for (const [field, value] of [
    ['GITHUB_SHA', 'd0f309e'], ['GITHUB_REPOSITORY', 'someone/fork'], ['GITHUB_REF_TYPE', 'branch'],
    ['GITHUB_REF_NAME', 'v1.0.0'], ['GITHUB_REF_NAME', 'preview/h3-'], ['GITHUB_REF_NAME', 'preview/h3-RC1'], ['CI_RUN_ID', ''],
  ]) assert.ok(checkInputs({ ...good, [field]: value }).length > 0, `${field}=${value} must be refused`);
  assert.ok(TAG_PATTERN.test('preview/h3-rc1') && !TAG_PATTERN.test('preview/h3-../x'));
});

test('G1 服务器预检脚本严格只读', () => {
  const sh = readFileSync('deploy/scripts/h3-preview-precheck.sh', 'utf8');
  const code = sh.split('\n').filter((l) => !/^\s*#/.test(l) && !/^\s*echo /.test(l)).join('\n');
  for (const verb of [/compose[^\n|]*\s(up|down|rm|restart|stop|start|create|pull|exec|run)\b/, /(docker|\$DOCKER) (rm|stop|kill|restart|run|exec|pull|push|network (create|rm|connect|disconnect)|volume (create|rm|prune)|system prune)/i,
    /systemctl (start|stop|restart|reload|enable|disable)/, /nginx -s/, /certbot/, /\bpsql\b/, /\bcat [^\n]*secrets/, />\s*\/(etc|srv)\//, /\btee\b/, /\bchmod\b|\bchown\b/])
    assert.ok(!verb.test(code), `precheck must not contain ${verb}`);
  assert.match(sh, /PRECHECK_MODE=READ_ONLY/);
  assert.match(sh, /PRECHECK_BLOCKERS=/);
});
