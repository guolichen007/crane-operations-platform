import type { WebSession } from '../../../../contracts/typescript/types.gen';
export type AuthState = 'AUTH_LOADING' | 'UNAUTHENTICATED' | 'AUTHENTICATED' | 'AUTH_ERROR';
export function authState(loading: boolean, session: WebSession | null, error = ''): AuthState {
  if (loading) return 'AUTH_LOADING';
  if (error) return 'AUTH_ERROR';
  return session ? 'AUTHENTICATED' : 'UNAUTHENTICATED';
}
export function isViewerOnly(session: WebSession | null): boolean {
  return (
    !!session && session.projects.length > 0 && session.projects.every((p) => p.role === 'viewer')
  );
}
