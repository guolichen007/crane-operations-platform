import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import postcss, { type ChildNode } from 'postcss';

// Guard against a stylesheet edit leaving an unclosed rule: browsers then parse the
// following rules as nested selectors and silently drop the layout they define
// (CI run 42006948812: `.rail-sep {` without `}` broke the overview scene dock).
const SRC = join(__dirname, '..', 'src');
function cssFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? cssFiles(join(dir, e.name)) : e.name.endsWith('.css') ? [join(dir, e.name)] : []
  );
}
function nestedRules(nodes: ChildNode[] | undefined, out: string[], inRule: boolean) {
  for (const n of nodes ?? []) {
    if (n.type === 'rule') {
      if (inRule) out.push(n.selector);
      nestedRules(n.nodes, out, true);
    } else if (n.type === 'atrule') {
      nestedRules(n.nodes, out, inRule);
    }
  }
}

describe('样式表结构', () => {
  const files = cssFiles(SRC);
  it('找到样式表', () => expect(files.length).toBeGreaterThan(3));
  for (const f of files) {
    it(`${f.slice(SRC.length + 1)} 可解析且无意外嵌套规则`, () => {
      const root = postcss.parse(readFileSync(f, 'utf8'), { from: f });
      const nested: string[] = [];
      nestedRules(root.nodes, nested, false);
      expect(nested).toEqual([]);
    });
  }
  it('反证：未闭合规则会被识别为嵌套', () => {
    const nested: string[] = [];
    nestedRules(postcss.parse('.a {\n.b { color: red; }\n}').nodes, nested, false);
    expect(nested).toEqual(['.b']);
  });
});
