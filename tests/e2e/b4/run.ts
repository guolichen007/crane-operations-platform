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
 * When a required environment name is configured (Preview UAT), assert the
 * server-side sentinel matches before any write. Must be called after login.
 * Unset requirement (isolated CI fixture) skips the check.
 */
export async function assertWriteTarget(page: Page): Promise<void> {
  const required = process.env.B4_REQUIRED_ENVIRONMENT;
  if (!required) return;
  const environment = await page.evaluate(async () => {
    const response = await fetch('/api/v1/environment');
    return response.ok ? ((await response.json()) as { environment: string }).environment : `HTTP ${response.status}`;
  });
  expect(environment, `server environment must be "${required}" before any write`).toBe(required);
}
