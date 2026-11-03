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
