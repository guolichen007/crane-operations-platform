import { api } from './client';
import type {
  PortfolioScope,
  ProjectIntake,
  ProjectIntakeInput,
  Resource,
  ResourceReservation,
  ScheduleItem,
  ScheduleConflict,
  ScheduleDependency,
  ScheduleBaseline,
  PortfolioProject,
  ProjectMember,
  ProjectGrantChange,
  ProjectGrantResult,
  PendingInventoryProject,
  ProjectInventoryRegistration,
  InventoryProject,
} from '../../../../contracts/typescript/types.gen';

export const listScopes = () => api<PortfolioScope[]>('portfolio/scopes');

export const listIntakes = () => api<ProjectIntake[]>('portfolio/intakes');
export const getIntake = (id: string) => api<ProjectIntake>(`portfolio/intakes/${encodeURIComponent(id)}`);
export const createIntake = (input: ProjectIntakeInput) =>
  api<ProjectIntake>('portfolio/intakes', { method: 'POST', body: input });
export const transitionIntake = (id: string, status: string, reason: string) =>
  api<{ status: string }>(`portfolio/intakes/${encodeURIComponent(id)}/transitions`, {
    method: 'POST',
    body: { status, reason },
  });
export const approveIntake = (id: string) =>
  api<{ projectId: string }>(`portfolio/intakes/${encodeURIComponent(id)}/approve`, { method: 'POST', body: {} });

export const listResources = () => api<Resource[]>('portfolio/resources');
export const createResource = (input: Resource) =>
  api<Resource>('portfolio/resources', { method: 'POST', body: input });
export const getResourceCalendar = (id: string) =>
  api<{ timezone: string; rules: unknown[] }>(`portfolio/resources/${encodeURIComponent(id)}/calendar`);
export const putResourceCalendar = (id: string, timezone: string, version: number, rules: unknown[]) =>
  api<{ status: string }>(`portfolio/resources/${encodeURIComponent(id)}/calendar`, {
    method: 'PUT',
    body: { timezone, version, rules },
  });

export const listScheduleItems = () => api<ScheduleItem[]>('portfolio/schedule/items');
export const createScheduleItem = (input: ScheduleItem) =>
  api<ScheduleItem>('portfolio/schedule/items', { method: 'POST', body: input });
export const createReservation = (itemId: string, input: ResourceReservation) =>
  api<ResourceReservation>(`portfolio/schedule/items/${encodeURIComponent(itemId)}/reservations`, {
    method: 'POST',
    body: input,
  });
export const approveSchedule = (itemIds: string[]) =>
  api<{ status: string }>('portfolio/schedule/approve', { method: 'POST', body: { itemIds } });

export const listDependencies = () => api<ScheduleDependency[]>('portfolio/schedule/dependencies');
export const createDependency = (input: ScheduleDependency) =>
  api<ScheduleDependency>('portfolio/schedule/dependencies', { method: 'POST', body: input });

export const listConflicts = () => api<ScheduleConflict[]>('portfolio/conflicts');
export const overrideConflict = (id: number, reason: string) =>
  api<{ status: string }>(`portfolio/conflicts/${id}/override`, { method: 'POST', body: { reason } });

export const listBaselines = () => api<ScheduleBaseline[]>('portfolio/baselines');

// Q2 formal projects (approved intakes) and their initial ProjectGrants.
export const listPortfolioProjects = () => api<PortfolioProject[]>('portfolio/projects');
export const getPortfolioProject = (id: string) => api<PortfolioProject>(`portfolio/projects/${encodeURIComponent(id)}`);
export const listProjectMembers = (id: string) => api<ProjectMember[]>(`portfolio/projects/${encodeURIComponent(id)}/members`);
export const changeProjectMember = (id: string, change: ProjectGrantChange) =>
  api<ProjectGrantResult>(`portfolio/projects/${encodeURIComponent(id)}/members`, { method: 'POST', body: change });

// Q3 formal project → Inventory registration (project grant with location_write).
export const listPendingInventoryProjects = () => api<PendingInventoryProject[]>('inventory/pending-projects');
export const registerProjectInventory = (projectId: string, body: ProjectInventoryRegistration) =>
  api<InventoryProject>(`projects/${encodeURIComponent(projectId)}/inventory-registration`, { method: 'POST', body });
