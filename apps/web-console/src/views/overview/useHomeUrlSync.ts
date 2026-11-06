// URL ↔ workspace synchronization for the homepage. Direction is fixed:
//   route.query  ──apply──▶  store (regionTarget / selection / status / view)
//   store        ──write──▶  route.query (only when homeQueryFor says it differs)
// The scene never touches the URL; it reports to the store. Because the write
// compares against the current query and the apply only sets values that differ,
// a write never triggers an apply that writes again (no loop).
import { watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useOperations } from '../../stores/operations';
import { sceneProvider } from '../../scene/provider';
import { parseHomeQuery, homeQueryFor } from '../../scene/home-url';
import { regionPathOf, samePath } from '../../scene/region-path';
import { loadGeoManifest, type GeoManifest } from '../../scene/geo-manifest';
import { productionAuth, webSession } from '../../cloud/session';

export function useHomeUrlSync() {
  const route = useRoute();
  const router = useRouter();
  const store = useOperations();
  let manifest: GeoManifest | null = null;
  let applying = false;
  let applySeq = 0;
  // Set when apply() rejected/corrected part of the URL (unknown region, missing
  // asset, bad status). The next write must REPLACE the bad entry: pushing would
  // leave it in history, and Back would land on it, correct it and push again —
  // a back-button trap.
  let correcting = false;

  async function apply() {
    if (route.path !== '/overview') return;
    const seq = ++applySeq;
    const q = parseHomeQuery(route.query);
    applying = true;
    try {
      if (q.view !== store.view && !store.webglUnavailable) store.view = q.view;
      if (q.statusMalformed) {
        store.notice = '无效的状态过滤参数，已忽略。';
        correcting = true;
      }
      const status = q.status ?? 'all';
      if (status !== store.status) store.status = status;

      // Region: resolve the full path from the authorized manifest; unknown → country + notice.
      let target: number[] = [];
      if (q.region !== null || q.regionMalformed) {
        if (!manifest) {
          try {
            manifest = await loadGeoManifest();
          } catch {
            manifest = null;
          }
          if (seq !== applySeq) return; // a newer navigation superseded this one
        }
        const path = q.region !== null && manifest ? regionPathOf(q.region, manifest) : null;
        if (path) target = path;
        else {
          store.notice = `行政区 ${String(route.query.region)} 不存在或不可用，已回到全国。`;
          correcting = true;
        }
      }
      if (!samePath(target, store.regionTarget)) store.navigateRegion(target);

      // Selection: wait for provider data before deciding an id is unknown.
      if (q.asset || q.project) await sceneProvider.ensureLoaded().catch(() => undefined);
      if (seq !== applySeq) return;
      if (q.asset) {
        const d = sceneProvider.devices.value.find((x) => x.deviceId === q.asset);
        if (!d) {
          store.notice = `设备 ${q.asset} 不存在或不在您的授权范围内。`;
          correcting = true;
          store.select(null);
        } else if (store.selectedId !== d.deviceId) store.requestFocus('device', d.deviceId);
      } else if (q.project) {
        const p = sceneProvider.projects.value.find((x) => x.id === q.project);
        const granted = !productionAuth || !!webSession.value?.projects.some((g) => g.projectId === q.project);
        if (p) {
          if (store.selectedProjectId !== p.id) store.requestFocus('project', p.id);
        } else if (granted) {
          // Authorized project without an Inventory record: keep the user's
          // choice in the URL (the command bar offers it), say why nothing is shown.
          store.notice = `项目 ${q.project} 暂无 Inventory 空间数据，地图无法定位。`;
          if (store.selectedProjectId !== q.project) store.selectProject(q.project);
        } else {
          store.notice = `项目 ${q.project} 不存在或不在您的授权范围内。`;
          correcting = true;
          store.selectProject(null);
        }
      } else if (store.selectedId || store.selectedProjectId) store.select(null);
    } finally {
      if (seq === applySeq) applying = false;
    }
    // A correction may leave the store unchanged (e.g. an unknown region while
    // already at the country level), so no store watcher fires: serialize now.
    if (seq === applySeq && correcting) write();
  }

  function write() {
    // A pending focus intent (deep link / list click) has not set the selection
    // yet: serializing now would drop ?project= / ?asset= when the region changes
    // in the same navigation (H1-R1 finding). The watcher below re-runs write()
    // once the active view consumes the request.
    if (applying || route.path !== '/overview' || store.focusRequest) return;
    const w = homeQueryFor(
      {
        regionPath: store.regionPath,
        regionTarget: store.regionTarget,
        selectedId: store.selectedId,
        selectedProjectId: store.selectedProjectId,
        status: store.status,
        view: store.view,
      },
      route.query
    );
    if (!w) return;
    const history = correcting ? 'replace' : w.history;
    correcting = false;
    void (history === 'push' ? router.push({ path: '/overview', query: w.query }) : router.replace({ path: '/overview', query: w.query }));
  }

  watch(() => route.query, () => void apply(), { immediate: true });
  watch(
    () => [store.regionPath.join(','), store.regionTarget.join(','), store.selectedId, store.selectedProjectId, store.status, store.view, store.focusRequest?.seq ?? 0],
    () => write()
  );
}
