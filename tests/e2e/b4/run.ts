import { type Page, expect } from '@playwright/test';
import { randomBytes } from 'node:crypto';

// One prefix per test-process run. Persistent environments (Preview) keep data
// between runs and asset codes are project-unique, so fixed codes would collide
// on the second run. The CI fixture pins it via B4_RUN_ID so its DB verifier can
// find the CRUD records; otherwise it is random.
export const RUN_ID = (process.env.B4_RUN_ID || randomBytes(4).toString('hex')).toUpperCase();
if (!/^[A-Z0-9]{4,16}$/.test(RUN_ID)) throw new Error('B4_RUN_ID must be 4-16 alphanumeric characters');

/** A run-unique business code, e.g. code('CRN', 'CRUD') → CRN-B4-1A2B3C4D-CRUD. */
export const code = (kind: string, label: string) => `${kind}-B4-${RUN_ID}-${label}`;

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
