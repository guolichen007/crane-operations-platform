// Fail-closed E2E target guard for the Preview UAT. The Preview browser suite
// performs real business writes, so it must only run against the isolated
// Preview origin over HTTPS, and only when the operator has explicitly declared
// the expected server-side environment. Any deviation aborts at configuration
// load — before a browser is launched.

export const PREVIEW_ORIGIN = 'https://b4.crane.youdaozd.com';
export const PREVIEW_HOST = 'b4.crane.youdaozd.com';
export const PREVIEW_ENVIRONMENT = 'b4-preview';

/**
 * The target must be exactly https://b4.crane.youdaozd.com (an optional single
 * trailing "/" is tolerated). Explicit ports (even :443), paths, query, fragment,
 * userinfo, other hosts, trailing-dot hosts and case variants are rejected. The
 * raw string is compared rather than a parsed URL because URL parsing
 * normalizes some of these forms away.
 */
export function assertPreviewTarget(origin: string | undefined): string {
  if (!origin) {
    throw new Error('A1_WEB_ORIGIN is required for Preview UAT (refusing to run without an explicit target)');
  }
  if (!origin.startsWith('https://')) {
    throw new Error(`Preview UAT requires HTTPS (refusing "${origin}")`);
  }
  if (origin !== PREVIEW_ORIGIN && origin !== PREVIEW_ORIGIN + '/') {
    throw new Error(`Preview UAT target must be exactly ${PREVIEW_ORIGIN} (refusing "${origin}"); production is never a UAT target`);
  }
  return PREVIEW_ORIGIN;
}

/** The operator must declare the expected server environment before any run. */
export function assertRequiredEnvironment(value: string | undefined): string {
  if (value !== PREVIEW_ENVIRONMENT) {
    throw new Error(`B4_REQUIRED_ENVIRONMENT must be "${PREVIEW_ENVIRONMENT}" for Preview UAT (got ${value ? `"${value}"` : 'nothing'})`);
  }
  return value;
}
