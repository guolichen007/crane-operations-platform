import { QueryClient } from '@tanstack/vue-query';

// Single shared QueryClient for the workspace. Mutations invalidate with precise
// query keys (['assets', project, filters], ['asset', id], ...) rather than a
// blanket refetch.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
    mutations: { retry: 0 },
  },
});

/**
 * Q4-QA-R1: the cache is keyed by resource, not by identity. When the login identity or its project
 * grants change (cloud/session.ts → 'cloud-identity-changed'), every cached result of the previous
 * scope is dropped: resetQueries cancels in-flight fetches (their late responses are discarded),
 * returns every query to its initial state (no previous data, no "stale" fallback) and refetches the
 * mounted ones under the new identity. Pages render their loading state meanwhile.
 */
export function resetForIdentityChange() {
  void queryClient.resetQueries().catch(() => undefined);
}
if (typeof window !== 'undefined') window.addEventListener('cloud-identity-changed', resetForIdentityChange);

/**
 * PortfolioGrant (portfolio_memberships) is NOT part of WebSession (whose `projects` are
 * ProjectGrants), so losing or downgrading a portfolio role does not raise 'cloud-identity-changed'.
 * Q4-QA-R1a: whenever the server signals that the caller's portfolio scope may have changed (a 403
 * on a portfolio action, or entering the portfolio page), every cached portfolio result is reset —
 * the previous data is dropped, not kept as stale — and re-read under the caller's current scope.
 */
export const PORTFOLIO_QUERY_KEYS = ['portfolio-scopes', 'intakes', 'resources', 'schedule-items', 'conflicts', 'portfolio-projects', 'project-members'] as const;
export function resetPortfolioScope() {
  return queryClient
    .resetQueries({ predicate: (q) => (PORTFOLIO_QUERY_KEYS as readonly string[]).includes(String(q.queryKey[0])) })
    .catch(() => undefined);
}
