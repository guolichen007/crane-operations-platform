// G3-3A pipeline helpers. Pure functions, unit tested in pipeline.test.ts.
import { stages } from './model';

export const stageCodes = Object.keys(stages);
export const progressStages = stageCodes.slice(0, 9); // L0..L8

// Mirror of the server's validStage() for statuses where a stage change is
// allowed (DRAFT/RETURNED: any other stage; SUBMITTED/UNDER_REVIEW/APPROVED:
// one step forward, HOLD/LOST with a reason, WON only from L8, HOLD only back
// to L1). The server stays authoritative; this only builds the choice list.
export function nextStages(from: string, intakeStatus: string): string[] {
  const editable = intakeStatus === 'DRAFT' || intakeStatus === 'RETURNED';
  if (editable) return stageCodes.filter((s) => s !== from);
  if (!['SUBMITTED', 'UNDER_REVIEW', 'APPROVED'].includes(intakeStatus)) return [];
  if (from === 'WON' || from === 'LOST') return [];
  if (from === 'HOLD') return ['L1_REQUIREMENT'];
  const out: string[] = [];
  const i = progressStages.indexOf(from);
  if (i >= 0 && i < 8) out.push(progressStages[i + 1]);
  if (from === 'L8_PENDING_CONTRACT') out.push('WON');
  out.push('HOLD', 'LOST');
  return out;
}

export type FollowupState = 'overdue' | 'scheduled' | 'unscheduled';
// Only the real next follow-up time decides; updatedAt is never used.
export function followupState(nextFollowupAt: string | null | undefined, asOf: string): FollowupState {
  if (!nextFollowupAt) return 'unscheduled';
  const at = Date.parse(nextFollowupAt);
  if (Number.isNaN(at)) return 'unscheduled';
  return at < (asOf ? Date.parse(asOf) : Date.now()) ? 'overdue' : 'scheduled';
}
export const followupLabels: Record<FollowupState, string> = {
  overdue: '已逾期',
  scheduled: '已安排',
  unscheduled: '未安排下一次跟进',
};

// Base filters shared by /stage-summary and every column request.
export interface PipelineFilters {
  q: string;
  status: string;
  province: string;
  updatedAfter: string; // RFC3339 or ''
}
export function filterQuery(f: PipelineFilters): URLSearchParams {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  return p;
}
export function columnQuery(f: PipelineFilters, stage: string, page: number, pageSize: number): string {
  const p = filterQuery(f);
  p.set('stage', stage);
  p.set('page', String(page));
  p.set('pageSize', String(pageSize));
  return p.toString();
}
