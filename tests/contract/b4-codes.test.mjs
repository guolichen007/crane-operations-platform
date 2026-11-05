// B4 business-code uniqueness (static): every code('KIND', label) used by the B4
// browser specs must be unique across tests within one B4_RUN_ID. Asset and
// equipment codes are unique per project in the database, so two tests deriving
// the same code collide (run 42050722509: nav 'VIS1280' vs visual `VIS${w}`).
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createCodeRegistry } from '../e2e/b4/codes.ts';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const dir = join(root, 'tests', 'e2e', 'b4');

/** Offsets of each top-level test(...) / for-loop test block, to attribute a call site to its test. */
function testStarts(src) {
  return [...src.matchAll(/^(?:test|for)\b/gm)].map((m) => m.index);
}
function ownerOf(starts, offset) {
  let owner = -1;
  for (const s of starts) if (s <= offset) owner = s;
  return owner;
}

/** All code sites: literal labels, dynamic label prefixes, and helper calls that wrap code('CRN', label). */
export function collectSites(files) {
  const literal = [];
  const dynamic = [];
  for (const { name, src } of files) {
    const starts = testStarts(src);
    const at = (i) => `${name}#${ownerOf(starts, i)}`;
    for (const m of src.matchAll(/\bcode\(\s*'([A-Z]+)'\s*,\s*([^)]*?)\)/g)) {
      const [, kind, arg] = m;
      const lit = /^'([^']*)'$/.exec(arg.trim());
      if (lit) {
        literal.push({ kind, label: lit[1], owner: at(m.index), where: `${name}:${src.slice(0, m.index).split('\n').length}` });
        continue;
      }
      const tpl = /^`([^`$]*)\$\{/.exec(arg.trim()) || /^'([^']*)'\s*\+/.exec(arg.trim());
      if (tpl) dynamic.push({ kind, prefix: tpl[1], owner: at(m.index), where: `${name}:${src.slice(0, m.index).split('\n').length}` });
      // A bare variable (e.g. `label` inside a helper) is covered by its callers below.
    }
    // Helpers that build code('CRN', label) from their second argument.
    for (const m of src.matchAll(/\b(?:createViaApi|createAsset)\(\s*page\s*,\s*'([^']+)'/g)) {
      literal.push({ kind: 'CRN', label: m[1], owner: at(m.index), where: `${name}:${src.slice(0, m.index).split('\n').length}` });
    }
    for (const m of src.matchAll(/\bcreateAsset\(\s*page\s*,\s*`([^`$]*)\$\{/g)) {
      dynamic.push({ kind: 'CRN', prefix: m[1], owner: at(m.index), where: `${name}:${src.slice(0, m.index).split('\n').length}` });
    }
  }
  return { literal, dynamic };
}

export function findCollisions({ literal, dynamic }) {
  const problems = [];
  const byCode = new Map();
  for (const s of literal) {
    const key = `${s.kind}-${s.label}`;
    const prev = byCode.get(key);
    if (prev && prev.owner !== s.owner) problems.push(`${key}: ${prev.where} and ${s.where}`);
    if (!prev) byCode.set(key, s);
  }
  // A dynamic label `PREFIX${x}` may produce any PREFIX<suffix>; a literal label
  // from another test starting with the same prefix can collide with it.
  for (const d of dynamic) {
    for (const s of literal) {
      if (s.kind === d.kind && s.owner !== d.owner && s.label.startsWith(d.prefix)) {
        problems.push(`${s.kind}-${s.label} (${s.where}) may equal dynamic ${d.kind}-${d.prefix}… (${d.where})`);
      }
    }
  }
  for (let i = 0; i < dynamic.length; i++)
    for (let j = i + 1; j < dynamic.length; j++) {
      const a = dynamic[i], b = dynamic[j];
      if (a.kind === b.kind && a.owner !== b.owner && (a.prefix.startsWith(b.prefix) || b.prefix.startsWith(a.prefix)))
        problems.push(`dynamic ${a.kind}-${a.prefix}… (${a.where}) overlaps ${b.kind}-${b.prefix}… (${b.where})`);
    }
  return problems;
}

test('B4 用例业务编码在同一 RUN_ID 内跨用例唯一', () => {
  const files = readdirSync(dir)
    .filter((f) => f.endsWith('.spec.ts'))
    .map((f) => ({ name: f, src: readFileSync(join(dir, f), 'utf8') }));
  assert.ok(files.length >= 4, 'B4 specs found');
  const sites = collectSites(files);
  assert.ok(sites.literal.length >= 10, 'code sites found');
  assert.deepEqual(findCollisions(sites), []);
});

test('反证：静态检查能发现字面量与动态标签碰撞', () => {
  const files = [
    { name: 'nav.spec.ts', src: "test('a', async () => {\n  code('CRN', 'VIS1280');\n});\n" },
    { name: 'visual.spec.ts', src: "for (const w of [1280]) {\n  test('b', async () => { code('CRN', `VIS${w}`); });\n}\n" },
    { name: 'x.spec.ts', src: "test('c', async () => { code('CRN', 'DUP'); });\ntest('d', async () => { code('CRN', 'DUP'); });\n" },
  ];
  const problems = findCollisions(collectSites(files));
  assert.equal(problems.length, 2, problems.join('\n'));
  assert.match(problems.join('\n'), /CRN-VIS1280/);
  assert.match(problems.join('\n'), /CRN-DUP/);
  // Same code reused inside one test is allowed.
  assert.deepEqual(findCollisions(collectSites([{ name: 'y.spec.ts', src: "test('e', async () => { code('EQ', 'A'); code('EQ', 'A'); });\n" }])), []);
});

test('运行期登记：不同用例取同一编码立即失败，同一用例重复使用允许', () => {
  const r = createCodeRegistry();
  r.claim('CRN-B4-X-A', 'nav › t1');
  assert.equal(r.claim('CRN-B4-X-A', 'nav › t1'), 'CRN-B4-X-A');
  assert.throws(() => r.claim('CRN-B4-X-A', 'visual › t2'), /used by two tests/);
});
