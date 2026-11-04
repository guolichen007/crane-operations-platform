import type { ProjectGrant, WebSession } from '../../../../contracts/typescript/types.gen';
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

// Frozen project capability matrix (internal/identity/capability.go): only
// platform_admin holds asset_master_write / location_write / catalog_write /
// asset_retire. operator keeps watch+maintenance_request_create; maintainer adds
// inspection/maintenance/wire-rope/document write but stays asset-master
// read-only. The frontend never widens these permissions.
const WRITABLE_ROLE = 'platform_admin';

// writableProjects returns the caller's project grants that carry asset write.
export function writableProjects(session: WebSession | null): ProjectGrant[] {
  return session?.projects.filter((p) => p.role === WRITABLE_ROLE) ?? [];
}
// canWriteProject mirrors HasCapability(role, CapAssetMasterWrite): true only for
// platform_admin on that project. operator/maintainer remain asset read-only.
export function canWriteProject(session: WebSession | null, projectId: string): boolean {
  if (!session) return false;
  return session.projects.some((p) => p.projectId === projectId && p.role === WRITABLE_ROLE);
}
