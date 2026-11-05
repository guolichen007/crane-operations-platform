// Fail-closed E2E target guard for the Preview UAT. The Preview browser suite
// performs real business writes, so it must only run against the isolated
// Preview origin over HTTPS. Missing target, HTTP, or the production host all
// abort at configuration load — before any browser action.

export const PREVIEW_HOST = 'b4.crane.youdaozd.com';

export function assertPreviewTarget(origin: string | undefined): string {
  if (!origin) {
    throw new Error('A1_WEB_ORIGIN is required for Preview UAT (refusing to run without an explicit target)');
  }
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    throw new Error(`invalid A1_WEB_ORIGIN "${origin}"`);
  }
  if (url.protocol !== 'https:') {
    throw new Error(`Preview UAT requires HTTPS, got "${url.protocol}//" (refusing HTTP)`);
  }
  if (url.hostname !== PREVIEW_HOST) {
    throw new Error(`Preview UAT refuses host "${url.hostname}" (must be ${PREVIEW_HOST}); production is never a UAT target`);
  }
  return origin;
}
