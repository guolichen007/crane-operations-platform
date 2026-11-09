// Vue binding of the operations summary controller (H2-1D-1). One controller for the app (API mode
// only — demo mode never creates it and never calls the production summary endpoint). The page binds
// the scope (login identity × displayed region × alarm window); the controller does the isolation.
import { onMounted, onUnmounted, watch, type ShallowRef } from 'vue';
import { getOperationsSummary, type AlarmWindow } from '../cloud/api';
import { productionAuth, webSession } from '../cloud/session';
import { loadGeoManifestVersion } from './geo-manifest';
import {
  createSummaryController,
  startSummaryPolling,
  summaryIdentity,
  type SummaryController,
  type SummarySnapshot,
} from './operations-summary-provider';

let instance: SummaryController | undefined;

/**
 * Administrative index check: the server's scope.geoIndexVersion must equal the SHA-256 of the
 * manifest the browser drills with. Different tables would put numbers under the wrong region, so a
 * mismatch blocks the numbers. When the browser cannot compute the version (manifest unavailable or no
 * SubtleCrypto) the numbers are shown and the version is reported as unverified.
 */
export const geoVersionState = { browser: null as string | null, checked: false };
async function verify(s: { scope: { geoIndexVersion: string } }): Promise<string | null> {
  const browser = await loadGeoManifestVersion().catch(() => null);
  geoVersionState.browser = browser;
  geoVersionState.checked = browser !== null;
  if (browser !== null && browser !== s.scope.geoIndexVersion)
    return `行政区数据版本不一致（服务端 ${s.scope.geoIndexVersion.slice(0, 8)}…，浏览器 ${browser.slice(0, 8)}…），为避免区域统计错位，暂不显示运营统计。请刷新页面或联系管理员。`;
  return null;
}

export function summaryController(): SummaryController {
  return (instance ??= createSummaryController({ fetch: getOperationsSummary, verify }));
}

/**
 * Binds the summary to the page: scope follows the login identity (logout, account switch, grant
 * change → cache dropped) and the given region/window; polls every 60 s while mounted; leaving the
 * page aborts the in-flight request.
 */
export function useOperationsSummary(region: () => number | null, window: () => AlarmWindow): ShallowRef<SummarySnapshot> {
  const c = summaryController();
  let stop = () => {};
  watch(
    () => [summaryIdentity(productionAuth, webSession.value), region(), window()] as const,
    ([identity, r, w]) => c.setScope({ identity, region: r, window: w }),
    { immediate: true }
  );
  onMounted(() => {
    void c.refresh(); // page entry: fresh numbers (shares the request started by setScope, if any)
    stop = startSummaryPolling(c);
  });
  onUnmounted(() => {
    stop();
    c.abort();
  });
  return c.snapshot;
}
