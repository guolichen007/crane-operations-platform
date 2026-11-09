// Synthetic OperationsSummary builder for unit and browser tests (H2-1D). Shape = the generated
// contract (contracts/typescript/types.gen.ts); every count defaults to 0 and is overridden per test.
// Test-only data — never a production measurement.
import type { OperationsSummary, OperationsRegion } from '../../../contracts/typescript/types.gen';

export const GEO_VERSION = '2c3397c7c966433b21adbad6f7465a427145783beced1e7beef7d0b68a06a634';

export type SummaryOverrides = {
  region?: number | null;
  regionName?: string;
  window?: OperationsSummary['freshness']['alarmWindow'];
  authorizedProjects?: number;
  projects?: number;
  registered?: number;
  draft?: number;
  outOfService?: number;
  noRuntime?: number;
  unlocated?: number;
  atScopeLevel?: number;
  boundReal?: number;
  boundSim?: number;
  boundTest?: number;
  realConnected?: number;
  pendingReview?: number;
  pendingReviewAlarm?: number;
  pendingReviewAssets?: OperationsSummary['metrics']['real']['pendingReviewAssets'];
  stale?: number;
  unknownTime?: number;
  offline?: number;
  live?: number;
  alarmReal?: number;
  alarmSim?: number;
  alarmTest?: number;
  alarmAll?: number;
  recordsTotal?: number;
  regions?: OperationsRegion[];
  geoIndexVersion?: string;
  sampledAt?: string;
  generatedAt?: string;
};

export function summary(o: SummaryOverrides = {}): OperationsSummary {
  const sampledAt = o.sampledAt ?? '2026-11-09T04:05:51.29023Z';
  const window = o.window ?? 'PT24H';
  const hours = { PT1H: 1, PT6H: 6, PT24H: 24, P7D: 168 }[window];
  const total = o.recordsTotal ?? 0;
  return {
    schemaVersion: 1,
    sampledAt,
    generatedAt: o.generatedAt ?? '2026-11-09T04:05:51.394031408Z',
    scope: {
      region: o.region ?? null,
      regionName: o.regionName ?? (o.region ? String(o.region) : '全部授权范围'),
      authorizedProjects: o.authorizedProjects ?? 1,
      geoIndexVersion: o.geoIndexVersion ?? GEO_VERSION,
    },
    freshness: {
      staleAfterSeconds: 2100,
      maxFutureSkewSeconds: 120,
      alarmWindow: window,
      windowStart: new Date(Date.parse(sampledAt) - hours * 3600_000).toISOString(),
      windowEnd: sampledAt,
    },
    metrics: {
      projects: o.projects ?? 1,
      assets: {
        registered: o.registered ?? 0,
        draft: o.draft ?? 0,
        outOfService: o.outOfService ?? 0,
        noRuntime: o.noRuntime ?? 0,
        unlocated: o.unlocated ?? 0,
        atScopeLevel: o.atScopeLevel ?? 0,
      },
      bound: { REAL: o.boundReal ?? 0, SIMULATION: o.boundSim ?? 0, TEST: o.boundTest ?? 0 },
      real: {
        connected: o.realConnected ?? 0,
        pendingReview: o.pendingReview ?? 0,
        pendingReviewAlarm: o.pendingReviewAlarm ?? 0,
        pendingReviewAssets: o.pendingReviewAssets ?? [],
        stale: o.stale ?? 0,
        unknownTime: o.unknownTime ?? 0,
        offline: o.offline ?? 0,
        live: o.live ?? 0,
      },
      operation: { status: 'NOT_AVAILABLE' },
      alarm: {
        devices: { REAL: o.alarmReal ?? 0, SIMULATION: o.alarmSim ?? 0, TEST: o.alarmTest ?? 0 },
        devicesAll: o.alarmAll ?? (o.alarmReal ?? 0) + (o.alarmSim ?? 0) + (o.alarmTest ?? 0),
        records: {
          cells: {
            REAL: { attributed: total, unattributed: 0 },
            SIMULATION: { attributed: 0, unattributed: 0 },
            TEST: { attributed: 0, unattributed: 0 },
          },
          total,
          assets: Math.min(total, o.registered ?? 0),
          futureSkew: 0,
        },
      },
      pendingEvents: { status: 'NOT_AVAILABLE' },
    },
    regions: o.regions ?? [],
  };
}
