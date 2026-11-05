import { type Page, type Locator, expect, test } from '@playwright/test';
import { createCodeRegistry } from './codes';
import { randomBytes } from 'node:crypto';

// One prefix per test-process run. Persistent environments (Preview) keep data
// between runs and asset codes are project-unique, so fixed codes would collide
// on the second run. The CI fixture pins it via B4_RUN_ID so its DB verifier can
// find the CRUD records; otherwise it is random.
export const RUN_ID = (process.env.B4_RUN_ID || randomBytes(4).toString('hex')).toUpperCase();
if (!/^[A-Z0-9]{4,16}$/.test(RUN_ID)) throw new Error('B4_RUN_ID must be 4-16 alphanumeric characters');

const registry = createCodeRegistry();
function currentOwner(): string {
  try {
    return test.info().titlePath.join(' › ');
  } catch {
    // Module-level code (computed when a spec file loads, used by one test).
    return 'module:' + (new Error().stack?.split('\n').find((l) => /\.spec\.ts/.test(l))?.trim() ?? 'unknown');
  }
}

/**
 * A run-unique business code, e.g. code('CRN', 'CRUD') → CRN-B4-1A2B3C4D-CRUD.
 * Two different tests requesting the same code in one run fail immediately.
 */
export const code = (kind: string, label: string) => registry.claim(`${kind}-B4-${RUN_ID}-${label}`, currentOwner());

/**
 * Strict asset creation for B4 specs: the response must be exactly 201 with a
 * non-empty id, the requested code and project. Any other outcome fails the test
 * immediately with the HTTP status and the server's error text (no cookies,
 * tokens or request headers are printed). A failed create can never be treated
 * as {id, version} and carried into the next step.
 */
export async function createAsset(
  page: Page,
  csrf: string,
  body: { projectId: string; assetType: string; assetCode: string; displayName?: string },
): Promise<{ id: string; version: number; assetCode: string; projectId: string }> {
  const result = await page.evaluate(async ({ csrf, body }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const text = await res.text();
    let json: Record<string, unknown> | null = null;
    try {
      json = JSON.parse(text) as Record<string, unknown>;
    } catch {
      json = null;
    }
    return { status: res.status, json, text: text.slice(0, 300) };
  }, { csrf, body });
  const error = typeof result.json?.error === 'string' ? result.json.error : result.text;
  expect(result.status, `POST /api/v1/assets ${body.assetCode} → HTTP ${result.status}: ${error}`).toBe(201);
  const created = result.json as { id?: string; version?: number; assetCode?: string; projectId?: string };
  expect(created.id, `created asset ${body.assetCode} has an id`).toBeTruthy();
  expect(created.assetCode).toBe(body.assetCode);
  expect(created.projectId).toBe(body.projectId);
  expect(typeof created.version).toBe('number');
  return created as { id: string; version: number; assetCode: string; projectId: string };
}

/**
 * Before any write, prove the server is the intended environment. Must be called
 * after login.
 *  - Preview origin: B4_REQUIRED_ENVIRONMENT is mandatory (also enforced by
 *    playwright.b4-preview.config.ts) and must match the server sentinel.
 *  - Isolated CI fixture (any other origin): the check runs only if the variable
 *    is set; the fixture's own isolation logic is unchanged.
 */
export async function assertWriteTarget(page: Page): Promise<void> {
  const required = process.env.B4_REQUIRED_ENVIRONMENT;
  const origin = new URL(page.url()).origin;
  if (origin === 'https://b4.crane.youdaozd.com' && required !== 'b4-preview') {
    throw new Error('Preview writes require B4_REQUIRED_ENVIRONMENT=b4-preview');
  }
  if (!required) return;
  const environment = await page.evaluate(async () => {
    const response = await fetch('/api/v1/environment');
    return response.ok ? ((await response.json()) as { environment: string }).environment : `HTTP ${response.status}`;
  });
  expect(environment, `server environment must be "${required}" before any write`).toBe(required);
}

/** Visible, inside the viewport, and the topmost element at its centre is itself (or a descendant). */
export async function expectUnobstructed(page: Page, target: Locator, label: string): Promise<void> {
  await expect(target, `${label} 可见`).toBeVisible();
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  expect(box, `${label} 有布局`).not.toBeNull();
  const vp = page.viewportSize()!;
  const cx = box!.x + box!.width / 2;
  const cy = box!.y + box!.height / 2;
  expect(cx >= 0 && cx <= vp.width && cy >= 0 && cy <= vp.height, `${label} 中心在视口内`).toBe(true);
  const handle = await target.elementHandle();
  const hit = await page.evaluate(
    ({ el, x, y }) => {
      const top = document.elementFromPoint(x, y);
      return !!top && (top === el || el!.contains(top));
    },
    { el: handle, x: cx, y: cy },
  );
  expect(hit, `${label} 未被浮层遮挡`).toBe(true);
}
