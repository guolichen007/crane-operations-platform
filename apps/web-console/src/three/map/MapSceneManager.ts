import {
  Scene,
  Group,
  Box3,
  Vector3,
  Color,
  MeshStandardMaterial,
  LineBasicMaterial,
  Mesh,
  SphereGeometry,
  MeshBasicMaterial,
  type WebGLRenderer,
} from 'three';
import { createRenderer } from '../core/Renderer';
import { CameraRig } from '../core/CameraRig';
import { SceneLoop } from '../core/SceneLoop';
import { GeoJsonLoader, type RegionEntry } from './GeoJsonLoader';
import { DrilldownController, type MapLevel } from './DrilldownController';
import { buildRegion } from './RegionMeshBuilder';
import { InteractionRouter, type InteractionPick } from './InteractionRouter';
import type { PickTarget } from './ScreenSpacePicker';
import { MapLabelLayer } from '../labels/MapLabelLayer';
import { addAtmosphere } from '../effects/SceneAtmosphere';
import { PremiumSceneBackground } from '../effects/PremiumSceneBackground';
import { SpatialFloor } from '../effects/SpatialFloor';
import { SceneIntroTimeline } from '../animation/SceneIntroTimeline';
import { CountryOuterGlow } from '../effects/CountryOuterGlow';
import { sceneTime } from '../materials/MapSideMaterial';
import { createPostProcessing } from '../effects/PostProcessing';
import { projectMarker } from '../markers/ProjectMarker';
import { provinceDeviceCounts } from '../markers/MarkerCluster';
import { RunningWave } from '../markers/RunningWave';
import { statusColor } from '../../data/types';
import { AlarmPulse } from '../markers/AlarmPulse';
import { NetworkLayer } from '../network/NetworkLayer';
import { project } from './GeoProjection';
import { disposeObject } from '../utils/dispose';
import { projects, devices } from '../../data';
import type { Project, Device } from '../../data/types';

type SceneCallbacks = {
  change: (stack: MapLevel[], children: RegionEntry[], state: string) => void;
  select: (deviceId: string) => void;
  project: (projectId: string) => void;
  error: (message: string) => void;
  stats: (stats: Record<string, string | number>) => void;
};
export class MapSceneManager {
  scene = new Scene();
  renderer: WebGLRenderer;
  rig: CameraRig;
  loader = new GeoJsonLoader();
  drill = new DrilldownController();
  private labels: MapLabelLayer;
  private loop: SceneLoop;
  private observer: ResizeObserver;
  private interaction: InteractionRouter;
  private pickTargets: PickTarget[] = [];
  private country = new Group();
  private detail = new Group();
  private markers = new Group();
  private network: NetworkLayer;
  private outer?: CountryOuterGlow;
  private floor = new SpatialFloor();
  private background = new PremiumSceneBackground();
  private intro?: SceneIntroTimeline;
  private pulses: AlarmPulse[] = [];
  private runningWaves: RunningWave[] = [];
  private active: Group[] = [];
  private hovered: Group | null = null;
  private width = 1;
  private height = 1;
  private alive = true;
  private focusedProjectId: string | null = null;
  private focusedDeviceId: string | null = null;
  private shownProjects = projects;
  private shownDevices = devices;
  private visibleGroups: Group[] = [];
  private lastStats = 0;
  private frames = 0;
  private countryBox = new Box3();
  private countrySamples: Vector3[] = [];
  private post: ReturnType<typeof createPostProcessing>;
  private revision = 0;
  readonly reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  private selectedAsset: string | null = null;
  private normalColor = new Color(0xffffff);
  private hoverColor = new Color(1.05, 1.05, 1.05);
  private currentTime = 0;
  private resizeTimer: ReturnType<typeof setTimeout> | undefined;
  constructor(
    private host: HTMLElement,
    private callbacks: SceneCallbacks
  ) {
    this.renderer = createRenderer();
    this.renderer.info.autoReset = false;
    host.append(this.renderer.domElement);
    this.rig = new CameraRig(this.renderer.domElement);
    this.labels = new MapLabelLayer(host, (event) => {
      const hit = this.interaction?.picker.pick(event.clientX, event.clientY);
      if (!hit) return false;
      this.interaction.lastPick = hit;
      this.routePick(hit);
      return true;
    });
    addAtmosphere(this.scene);
    this.scene.add(this.background.mesh, this.country, this.detail, this.markers, this.floor.group);
    this.network = new NetworkLayer(projects);
    this.scene.add(this.network.group);
    this.post = createPostProcessing(this.renderer, this.scene, this.rig.camera);
    this.interaction = new InteractionRouter(
      this.renderer.domElement,
      this.rig.camera,
      () => this.active,
      () => this.pickTargets,
      (g) => this.hover(g),
      (target) => this.routePick(target),
      () => this.drill.busy,
      (asset) => this.hoverAsset(asset?.id ?? null)
    );
    this.observer = new ResizeObserver(() => this.resize());
    this.observer.observe(host);
    this.resize();
    this.loop = new SceneLoop((dt, t) => this.update(dt, t));
  }
  async init() {
    try {
      await this.loader.init();
      const data = await this.loader.children(100000);
      if (!this.alive) return;
      for (const f of data.features) {
        if (!this.loader.manifest[Number(f.properties?.adcode)]) continue;
        const region = buildRegion(f);
        this.country.add(region);
      }
      this.outer = new CountryOuterGlow(this.country);
      this.outer.resize(this.width, this.height);
      this.scene.add(this.outer.group);
      this.active = this.country.children as Group[];
      this.visibleGroups = [...this.active];
      this.countryBox.setFromObject(this.country);
      this.countryBox.min.y = 0;
      this.countryBox.max.y = 2;
      this.countrySamples = this.geometrySamples(this.country);
      await this.rig.frame(
        this.countryBox,
        'COUNTRY_OVERVIEW',
        false,
        undefined,
        this.countrySamples
      );
      this.refreshLabelsAndMarkers();
      this.notify();
    } catch (e) {
      this.callbacks.error(String(e));
    }
  }
  private activateLabel(target: InteractionPick) {
    this.interaction.lastPick = target;
    this.routePick(target);
  }
  private routePick(target: InteractionPick) {
    if (target.kind === 'region' || target.kind === 'cluster') {
      this.callbacks.select('');
      void this.enter(Number(target.id));
      return;
    }
    if (target.kind === 'device') {
      const device = devices.find((d) => d.deviceId === target.id);
      if (device) void this.focusDevice(device);
      return;
    }
    this.callbacks.project(target.id);
  }
  private resize() {
    this.width = this.host.clientWidth;
    this.height = this.host.clientHeight;
    if (!this.width || !this.height) return;
    this.renderer.setSize(this.width, this.height);
    this.post.setSize(this.width, this.height);
    this.labels.renderer.setSize(this.width, this.height);
    this.rig.resize(this.width, this.height);
    this.outer?.resize(this.width, this.height);
    clearTimeout(this.resizeTimer);
    if (this.countrySamples.length)
      this.resizeTimer = setTimeout(() => void this.reframeCurrentView(), 120);
  }
  private async reframeCurrentView(transition = false) {
    if (!this.alive || this.drill.busy) return;
    const marker = this.focusedDeviceId
      ? this.markers.children.find((g) => g.userData.device?.deviceId === this.focusedDeviceId)
      : undefined;
    if (marker) {
      const box = new Box3().setFromObject(marker);
      box.expandByScalar(0.025);
      await this.rig.frame(box, 'DEVICE_FOCUS', transition, undefined, this.assetSamples(marker));
    } else if (this.focusedProjectId)
      await this.rig.frame(this.projectContextBox(), 'PROJECT_FOCUS', transition);
    else if (this.drill.current.level === 'country') {
      const center = new Vector3();
      let total = 0;
      for (const p of this.shownProjects) {
        const [x, z] = project(p.approximateLng, p.approximateLat);
        center.addScaledVector(new Vector3(x, 0, z), p.quantity);
        total += p.quantity;
      }
      if (total) center.divideScalar(total);
      await this.rig.frame(
        this.countryBox,
        'COUNTRY_OVERVIEW',
        transition,
        center,
        this.countrySamples
      );
    } else
      await this.rig.frame(
        this.drill.current.level === 'county'
          ? this.projectContextBox()
          : new Box3().setFromObject(this.detail),
        this.drill.current.level === 'province' ? 'PROVINCE_CONTEXT' : 'CITY_CONTEXT',
        transition,
        undefined,
        this.drill.current.level === 'county' ? undefined : this.geometrySamples(this.detail)
      );
    if (this.alive) this.notify();
  }
  private notify() {
    this.callbacks.change(
      [...this.drill.stack],
      this.loader.childrenEntries(this.drill.current.adcode),
      this.drill.state
    );
  }
  setSelection(id: string | null) {
    this.selectedAsset = id;
    if (this.drill.current.level === 'country' && !this.focusedProjectId) {
      const p =
        projects.find((p) => p.id === id) ??
        projects.find((p) => p.id === devices.find((d) => d.deviceId === id)?.id);
      for (const g of this.country.children as Group[])
        this.outline(g, 'selected', p?.adminPath[0] === g.userData.adcode ? 0.68 : 0);
    }
  }
  private hoverAsset(id: string | null) {
    for (const marker of this.markers.children)
      marker.userData.hovered =
        id !== null &&
        (marker.userData.device?.deviceId === id || marker.userData.project?.id === id);
  }
  private hover(g: Group | null) {
    if (this.drill.busy) return;
    if (this.hovered === g) return;
    if (this.hovered) {
      this.hovered.userData.lift = 0;
      this.outline(this.hovered, 'hover', 0);
    }
    this.hovered = g;
    if (g) {
      g.userData.lift = 0.04;
      this.outline(g, 'hover', 0.7);
      this.drill.state = 'REGION_HOVER';
    } else this.drill.state = this.drill.stack.length === 1 ? 'COUNTRY_IDLE' : 'REGION_IDLE';
    this.renderer.domElement.style.cursor = g ? 'pointer' : 'grab';
  }
  private outline(g: Group, key: 'hover' | 'selected', opacity: number) {
    const outline = g.userData[key] as Group | undefined;
    if (!outline) return;
    outline.visible = opacity > 0;
    outline.traverse((o) => {
      const m = (o as import('three').Line).material;
      if (m instanceof LineBasicMaterial) m.opacity = opacity;
    });
  }
  private dimCountry(code: number | null) {
    for (const g of this.country.children as Group[]) {
      const selected = g.userData.adcode === code;
      g.userData.lift = selected ? 0.5 : 0;
      g.traverse((o) => {
        const m = (o as import('three').Mesh).material;
        if (m instanceof MeshStandardMaterial) m.opacity = code ? (selected ? 0.62 : 0.13) : 1;
        else if (m instanceof LineBasicMaterial) m.opacity = code ? (selected ? 0.4 : 0.06) : 0.23;
      });
      this.outline(g, 'hover', 0);
      this.outline(g, 'selected', selected ? 0.68 : 0);
    }
  }
  async enter(code: number) {
    this.intro?.finish();
    if (!this.alive || this.drill.busy || code === this.drill.current.adcode) return;
    const entry = this.loader.manifest[code];
    if (!entry) return;
    // The manifest defines parent links, including province-administered counties.
    if (entry.parent !== this.drill.current.adcode) return;
    this.focusedProjectId = null;
    this.focusedDeviceId = null;
    this.drill.state = 'DRILLING_IN';
    this.hovered = null;
    this.notify();
    const revision = ++this.revision;
    try {
      const data = await this.loader.children(code);
      if (!this.alive || revision !== this.revision) return;
      this.dimCountry(this.loader.path(code)[0]?.adcode ?? code);
      this.country.visible = entry.level === 'province';
      disposeObject(this.detail);
      this.detail = new Group();
      this.scene.add(this.detail);
      const depth = entry.level === 'province' ? 0.42 : entry.level === 'prefecture' ? 0.27 : 0.18;
      for (const f of data.features) {
        const r = buildRegion(f, depth);
        r.position.y = 0.9;
        r.userData.base = 0.9;
        r.userData.lift = 0;
        this.detail.add(r);
      }
      this.detail.visible = false;
      this.active = entry.level === 'county' ? [] : (this.detail.children as Group[]);
      this.visibleGroups = [
        ...(this.country.children as Group[]),
        ...(this.detail.children as Group[]),
      ];
      this.drill.push(entry);
      this.network.group.visible = false;
      if (this.outer) this.outer.group.visible = false;
      this.refreshLabelsAndMarkers();
      const county = entry.level === 'county';
      const box = county ? this.projectContextBox() : new Box3().setFromObject(this.detail);
      await this.rig.frame(
        box,
        entry.level === 'province' ? 'PROVINCE_CONTEXT' : 'CITY_CONTEXT',
        !this.reduced,
        undefined,
        county ? undefined : this.geometrySamples(this.detail)
      );
      if (!this.alive) return;
      this.detail.visible = true;
      this.drill.state = 'REGION_IDLE';
      this.notify();
    } catch (e) {
      this.drill.state = 'REGION_IDLE';
      this.callbacks.error(String(e));
      this.notify();
    }
  }
  async back(index = 0) {
    this.intro?.finish();
    if (this.drill.busy || !this.alive) return;
    this.drill.state = 'DRILLING_OUT';
    this.notify();
    this.revision++;
    const destination = this.drill.stack[index];
    if (!destination) return;
    if (index === 0) {
      this.rig.countryMode = 'overview';
      this.focusedProjectId = null;
      this.focusedDeviceId = null;
      const ghost = this.scene.getObjectByName('China contour ghost silhouette');
      if (ghost) ghost.visible = true;
      this.drill.back(0);
      this.country.visible = true;
      disposeObject(this.detail);
      this.detail = new Group();
      this.scene.add(this.detail);
      this.active = this.country.children as Group[];
      this.visibleGroups = [...this.active];
      this.labels.clear();
      disposeObject(this.markers);
      this.markers = new Group();
      this.scene.add(this.markers);
      this.dimCountry(null);
      await this.rig.frame(
        this.countryBox,
        'COUNTRY_OVERVIEW',
        !this.reduced,
        undefined,
        this.countrySamples
      );
      if (!this.alive) return;
      this.drill.state = 'COUNTRY_IDLE';
      this.network.group.visible = true;
      this.refreshLabelsAndMarkers();
      this.notify();
    } else {
      this.drill.back(index - 1);
      this.drill.state = 'REGION_IDLE';
      await this.enter(destination.adcode);
    }
  }
  setFilter(items: Project[], deviceItems: Device[]) {
    this.intro?.finish();
    this.shownProjects = items;
    this.shownDevices = deviceItems;
    if (this.focusedProjectId && !items.some((p) => p.id === this.focusedProjectId)) {
      void this.back(0);
      this.callbacks.select('');
    }
    this.network.setProjects(
      items.filter(
        (p) =>
          p.dataSource === 'real' ||
          deviceItems.some((d) => d.id === p.id && d.status === 'RUNNING')
      )
    );
    this.refreshLabelsAndMarkers();
    if (!this.intro && this.outer)
      this.intro = new SceneIntroTimeline(
        this.currentTime,
        this.country,
        this.outer.group,
        this.markers,
        this.network.group,
        this.labels.renderer.domElement,
        this.reduced
      );
  }
  private refreshLabelsAndMarkers() {
    this.labels.clear();
    disposeObject(this.markers);
    this.markers = new Group();
    this.scene.add(this.markers);
    this.pulses = [];
    this.runningWaves = [];
    this.pickTargets = [];
    const current = this.drill.current;
    const country = current.level === 'country' && !this.focusedProjectId;
    const relevant = this.shownProjects.filter((p) =>
      this.focusedProjectId
        ? p.id === this.focusedProjectId
        : country || p.adminPath.includes(current.adcode)
    );
    const extent = country
      ? 80
      : new Box3().setFromObject(this.detail).getSize(new Vector3()).length();
    const scale = Math.max(0.045, Math.min(country ? 0.7 : 0.6, extent / 25));
    const top = country
      ? 0.81
      : current.level === 'province'
        ? 1.39
        : current.level === 'prefecture'
          ? 1.22
          : 1.13;
    const place = (p: Project, device?: Device, index = 0, label?: string) => {
      const g = projectMarker(p, device, index, top, scale);
      this.markers.add(g);
      g.userData.animationPhase = this.markers.children.length * 0.73;
      const displayStatus =
        device?.status ?? this.shownDevices.find((d) => d.id === p.id)?.status ?? 'STANDBY';
      g.userData.displayStatus = displayStatus;
      const beacon = g.userData.crane?.userData.beacon as
        | import('three').Mesh<import('three').CylinderGeometry, import('three').MeshBasicMaterial>
        | undefined;
      if (beacon) beacon.material.color.set(statusColor[displayStatus]);
      if (displayStatus === 'RUNNING') {
        const wave = new RunningWave(scale);
        g.add(wave.mesh);
        this.runningWaves.push(wave);
      }
      const kind = country
        ? p.dataSource === 'real'
          ? 'project'
          : 'cluster'
        : device
          ? 'device'
          : 'project';
      this.pickTargets.push({
        kind,
        id:
          kind === 'device' ? device!.deviceId : kind === 'cluster' ? String(p.adminPath[0]) : p.id,
        object: g,
        radius: kind === 'device' ? 40 : 38,
        centerY: g.userData.pickCenter,
        spanY: [0.02, g.userData.beacon.height + scale * 1.18],
      });
      const id = device?.deviceId ?? this.shownDevices.find((d) => d.id === p.id)?.deviceId ?? p.id;
      const text =
        label ??
        (country
          ? p.projectName
          : device
            ? `${String.fromCharCode(65 + index)}机`
            : `${p.projectName} · ${p.quantity}台`);
      const nameLabel = this.labels.add(
        g,
        text,
        (p.dataSource === 'real' ? 'map-project real' : 'map-project demo') +
          (device ? ' instance' : ''),
        g.userData.labelHeight,
        p.dataSource === 'real' ? 50 : displayStatus === 'ALARM' ? 25 : 15,
        () =>
          this.activateLabel({
            kind,
            id: kind === 'device' ? id : kind === 'cluster' ? String(p.adminPath[0]) : p.id,
            distancePx: 0,
          })
      );
      nameLabel.userData.assetId = device?.deviceId ?? p.id;
      if (p.dataSource === 'real') {
        const card = this.labels.add(g, '', 'map-hover-card', g.userData.labelHeight + 0.12, 100);
        const name = document.createElement('strong');
        name.textContent = p.projectName;
        const info = document.createElement('small');
        info.textContent = `${p.craneModel}\n${p.quantity} 台 · 计划 ${p.plannedImplementation.slice(0, 7)}`;
        card.element.replaceChildren(name, info);
        card.visible = false;
        card.userData.hoverCard = true;
      }
      if (
        device?.status === 'ALARM' ||
        (!device && this.shownDevices.some((d) => d.id === p.id && d.status === 'ALARM'))
      ) {
        const pulse = new AlarmPulse(scale);
        g.add(pulse.group);
        this.pulses.push(pulse);
      }
    };
    // The national scene contains one summary per province, never individual cases.
    if (country) {
      // Province anchors and counts are created with the geographic labels below.
    } else {
      for (const p of relevant) {
        if (current.level === 'province' && !this.focusedProjectId) place(p);
        else {
          this.shownDevices.filter((d) => d.id === p.id).forEach((d, i) => place(p, d, i));
          const [x, z] = project(p.approximateLng, p.approximateLat);
          const anchor = new Group();
          anchor.position.set(x, top + scale * 4, z);
          this.markers.add(anchor);
          this.labels.add(
            anchor,
            p.projectName,
            p.dataSource === 'real' ? 'map-project real' : 'map-project demo',
            0,
            10
          );
        }
      }
    }
    const regions = country
      ? (this.country.children as Group[])
      : (this.detail.children as Group[]);
    const provinceCounts = provinceDeviceCounts(this.shownDevices);
    for (const g of regions) {
      const e = this.loader.manifest[g.userData.adcode];
      if (!e?.center) continue;
      const centroid = country ? g.userData.feature?.properties?.centroid : undefined;
      const [x, z] = project(...((centroid ?? e.center) as [number, number]));
      const anchor = new Group();
      anchor.position.set(x, top + 0.1, z);
      this.markers.add(anchor);
      const count = provinceCounts.get(e.adcode) ?? 0;
      const shortName = e.name.replace(
        /壮族自治区|维吾尔自治区|回族自治区|特别行政区|自治区|省|市/g,
        ''
      );
      if (country && count > 0) {
        const dot = new Mesh(
          new SphereGeometry(0.075, 8, 6),
          new MeshBasicMaterial({
            color: '#88bbc5',
            transparent: true,
            opacity: 0.65,
            depthWrite: false,
          })
        );
        dot.position.y = 0.04;
        anchor.add(dot);
        this.pickTargets.push({
          kind: 'cluster',
          id: String(e.adcode),
          object: anchor,
          radius: 24,
          centerY: 0.04,
        });
      }
      const regionLabel = this.labels.add(
        anchor,
        country ? `${shortName} · ${count}台` : e.name,
        country ? 'map-region province-summary' : 'map-region',
        country ? 0.38 : 0,
        country ? 0 : 3,
        country
          ? undefined
          : () => {
              void this.enter(e.adcode);
            }
      );
      if (country) {
        regionLabel.userData.countryRegion = g.userData.adcode;
        regionLabel.element.dataset.provinceCode = String(e.adcode);
        regionLabel.element.dataset.deviceCount = String(count);
      }
    }
    for (const g of this.markers.children) {
      if (g.userData.project) {
        g.userData.spawn = this.currentTime;
        if (!this.reduced) g.scale.setScalar(0.02);
      }
    }
    if (country) {
      const anchor = new Group();
      anchor.position.copy(this.network.cloud);
      this.markers.add(anchor);
      const cloudLabel = this.labels.add(anchor, '云端节点', 'cloud-label', 0.85, 10);
      cloudLabel.element.title = 'CLOUD OPS · Virtual cloud node';
      cloudLabel.element.style.pointerEvents = 'auto';
    }
  }
  async focusProject(p: Project, shouldContinue: () => boolean = () => true, openPanel = true) {
    if (this.drill.busy || !shouldContinue()) return;
    this.intro?.finish();
    this.drill.state = 'DRILLING_IN';
    this.notify();
    const revision = ++this.revision;
    try {
      const data = await this.loader.children(p.adminPath[p.adminPath.length - 1]!);
      if (!this.alive || revision !== this.revision) return;
      if (!shouldContinue()) {
        this.drill.state = 'REGION_IDLE';
        this.notify();
        return;
      }
      const [fx, fz] = project(p.approximateLng, p.approximateLat);
      this.floor.setFocus(fx, fz);
      this.focusedProjectId = p.id;
      this.focusedDeviceId = null;
      this.country.visible = false;
      const ghost = this.scene.getObjectByName('China contour ghost silhouette');
      if (ghost) ghost.visible = false;
      disposeObject(this.detail);
      this.detail = new Group();
      this.scene.add(this.detail);
      for (const f of data.features) {
        const r = buildRegion(f, 0.18);
        r.position.y = 0.9;
        r.userData.base = 0.9;
        this.detail.add(r);
      }
      this.active = [];
      this.visibleGroups = this.detail.children as Group[];
      this.network.group.visible = false;
      this.refreshLabelsAndMarkers();
      await this.rig.frame(this.projectContextBox(), 'PROJECT_FOCUS', !this.reduced);
      if (!this.alive) return;
      this.drill.state = 'PROJECT_FOCUS';
      if (openPanel && shouldContinue()) this.callbacks.project(p.id);
      this.notify();
    } catch (e) {
      this.drill.state = 'REGION_IDLE';
      this.callbacks.error(String(e));
      this.notify();
    }
  }
  async focusDevice(device: Device) {
    if (this.drill.busy) return;
    if (this.focusedProjectId !== device.id) await this.focusProject(device, () => true, false);
    if (!this.alive) return;
    const marker = this.markers.children.find(
      (g) => g.userData.device?.deviceId === device.deviceId
    );
    if (!marker) return;
    // A focus can interrupt marker entrance. Fit its final geometry, not a temporary spawn scale.
    marker.scale.setScalar(1);
    delete marker.userData.spawn;
    this.focusedDeviceId = device.deviceId;
    this.drill.state = 'DRILLING_IN';
    this.notify();
    this.callbacks.select(device.deviceId);
    const box = new Box3().setFromObject(marker);
    box.expandByScalar(0.025);
    await this.rig.frame(box, 'DEVICE_FOCUS', !this.reduced, undefined, this.assetSamples(marker));
    if (!this.alive) return;
    this.drill.state = 'PROJECT_FOCUS';
    this.notify();
  }

  async setCountryView(mode: 'overview' | 'assets') {
    if (this.drill.current.level !== 'country' || this.drill.busy) return;
    this.intro?.finish();
    this.rig.countryMode = mode;
    const center = new Vector3();
    let total = 0;
    for (const p of this.shownProjects) {
      const [x, z] = project(p.approximateLng, p.approximateLat);
      center.addScaledVector(new Vector3(x, 0, z), p.quantity);
      total += p.quantity;
    }
    if (total) center.divideScalar(total);
    await this.rig.frame(
      this.countryBox,
      'COUNTRY_OVERVIEW',
      !this.reduced,
      center,
      this.countrySamples
    );
  }
  async setObservation(mode: 'operations' | 'cinematic') {
    if (this.drill.busy) return;
    this.intro?.finish();
    this.rig.observation = mode;
    await this.reframeCurrentView(!this.reduced);
    this.notify();
  }
  private projectContextBox() {
    const box = new Box3().setFromObject(this.detail);
    for (const g of this.markers.children)
      if (g.userData.project) {
        box.expandByPoint(g.position);
        box.expandByPoint(g.position.clone().add(new Vector3(0, g.userData.labelHeight, 0)));
      }
    box.expandByVector(box.getSize(new Vector3()).multiplyScalar(0.4));
    return box;
  }
  private projectedRegionBounds() {
    const group =
      this.drill.current.level === 'country' && !this.focusedProjectId ? this.country : this.detail;
    const points = this.geometrySamples(group);
    let minX = 1e9,
      maxX = -1e9,
      minY = 1e9,
      maxY = -1e9;
    for (const p of points) {
      p.project(this.rig.camera);
      minX = Math.min(minX, ((p.x + 1) * this.width) / 2);
      maxX = Math.max(maxX, ((p.x + 1) * this.width) / 2);
      minY = Math.min(minY, ((1 - p.y) * this.height) / 2);
      maxY = Math.max(maxY, ((1 - p.y) * this.height) / 2);
    }
    return { minX, maxX, minY, maxY, widthRatio: (maxX - minX) / this.width };
  }
  private geometrySamples(group: Group) {
    group.updateMatrixWorld(true);
    const samples: Vector3[] = [];
    for (const region of group.children) {
      const surface = region.children[1] as import('three').Mesh | undefined;
      if (!surface) continue;
      const positions = surface.geometry.getAttribute('position');
      for (let i = 0; i < positions.count; i++)
        samples.push(
          new Vector3().fromBufferAttribute(positions, i).applyMatrix4(surface.matrixWorld)
        );
    }
    return samples;
  }
  private regionCenters() {
    const rect = this.host.getBoundingClientRect();
    return this.active.flatMap((g) => {
      const r = this.loader.manifest[g.userData.adcode];
      if (!r?.center) return [];
      const [x, z] = project(...r.center);
      const point = new Vector3(x, g.position.y + 0.4, z).project(this.rig.camera);
      return [
        {
          adcode: r.adcode,
          x: rect.left + ((point.x + 1) * this.width) / 2,
          y: rect.top + ((1 - point.y) * this.height) / 2,
        },
      ];
    });
  }
  private sideSample() {
    const gd = this.country.children.find((r) => r.userData.adcode === 440000);
    const geo = gd?.userData.feature.geometry as import('geojson').MultiPolygon | undefined;
    if (!geo)
      return { source: 'not initialized', lng: 0, lat: 0, pixels: 0, top: [0, 0], bottom: [0, 0] };
    const ring = geo.coordinates.reduce((a, b) => (a[0]!.length > b[0]!.length ? a : b))[0]!;
    const coast = ring.reduce((a, b) =>
      Math.hypot(a[0]! - 115.2, a[1]! - 22.7) < Math.hypot(b[0]! - 115.2, b[1]! - 22.7) ? a : b
    );
    const [x, z] = project(coast[0]!, coast[1]!);
    const top = new Vector3(x, 0.72, z).project(this.rig.camera);
    const bottom = new Vector3(x, 0, z).project(this.rig.camera);
    return {
      source: 'cached Guangdong coastline vertex',
      lng: coast[0],
      lat: coast[1],
      pixels: Math.hypot(
        ((top.x - bottom.x) * this.width) / 2,
        ((top.y - bottom.y) * this.height) / 2
      ),
      top: [((top.x + 1) * this.width) / 2, ((1 - top.y) * this.height) / 2],
      bottom: [((bottom.x + 1) * this.width) / 2, ((1 - bottom.y) * this.height) / 2],
    };
  }
  private assetSamples(marker: import('three').Object3D) {
    const points: Vector3[] = [];
    marker.updateWorldMatrix(true, true);
    marker.traverse((o) => {
      const mesh = o as import('three').Mesh;
      const pos = mesh.geometry?.getAttribute('position');
      if (pos)
        for (let i = 0; i < pos.count; i++)
          points.push(new Vector3().fromBufferAttribute(pos, i).applyMatrix4(mesh.matrixWorld));
    });
    return points;
  }
  private focusedAssetBounds() {
    const marker = this.markers.children.find(
      (g) => g.userData.device?.deviceId === this.focusedDeviceId
    );
    if (!marker) return null;
    const ys = this.assetSamples(marker).map((p) => p.project(this.rig.camera).y);
    return { heightRatio: (Math.max(...ys) - Math.min(...ys)) / 2 };
  }
  diagnostics() {
    const points: { x: number; y: number }[] = [];
    const v = new Vector3();
    this.scene.updateMatrixWorld();
    for (const region of this.country.children) {
      const surface = region.children[1] as import('three').Mesh;
      const positions = surface.geometry.getAttribute('position');
      for (let i = 0; i < positions.count; i++) {
        v.fromBufferAttribute(positions, i)
          .applyMatrix4(surface.matrixWorld)
          .project(this.rig.camera);
        points.push({ x: ((v.x + 1) * this.width) / 2, y: ((1 - v.y) * this.height) / 2 });
      }
    }
    const minX = Math.min(...points.map((p) => p.x)),
      maxX = Math.max(...points.map((p) => p.x)),
      minY = Math.min(...points.map((p) => p.y)),
      maxY = Math.max(...points.map((p) => p.y));
    return {
      camera: {
        fov: this.rig.camera.fov,
        elevation: this.rig.elevation,
        observation: this.rig.observation,
      },
      focusedAssetBounds: this.focusedAssetBounds(),
      mapDepth: 0.72,
      bevelThickness: 0.055,
      projectedRatio: (maxY - minY) / (maxX - minX),
      sideSample: this.sideSample(),
      regionCenters: this.regionCenters(),
      selectedLiftMax: Math.max(0, ...this.country.children.map((g) => g.userData.lift ?? 0)),
      cameraTarget: this.rig.controls.getTarget(new Vector3()).toArray(),
      intro: { active: this.intro?.active ?? false, duration: 1.9 },
      animation: {
        sideFlowPhase: sceneTime.value * 0.57,
        floorPhase: sceneTime.value / 11,
        time: this.currentTime,
        network: this.network.snapshot(),
        spatialBeacons: this.markers.children
          .filter((g) => g.userData.beacon)
          .map((g) => ({
            id: g.userData.device?.deviceId ?? g.userData.project?.id,
            ...g.userData.beacon.snapshot(),
          })),
        beacons: this.markers.children
          .filter((g) => g.userData.displayStatus === 'RUNNING')
          .map((g) => ({
            id: g.userData.device?.deviceId ?? g.userData.project?.id,
            opacity: g.userData.crane?.userData.beacon?.material.opacity,
          })),
        alarmRings: this.pulses.flatMap((p) =>
          p.group.children.map((r) => ({
            scale: r.scale.x,
            opacity: (
              r as import('three').Mesh<
                import('three').RingGeometry,
                import('three').MeshBasicMaterial
              >
            ).material.opacity,
          }))
        ),
      },
      picks: this.interaction.picker.snapshot(),
      lastPick: this.interaction.lastPick,
      hoverLift: this.hovered?.userData.lift ?? 0,
      region: this.projectedRegionBounds(),
      preset: this.rig.preset,
      view: this.rig.countryMode,
      stage: { width: this.width, height: this.height },
      country: {
        minX,
        maxX,
        minY,
        maxY,
        width: maxX - minX,
        pageWidthRatio: (maxX - minX) / innerWidth,
      },
      adcode: this.drill.current.adcode,
    };
  }
  setTour(active: boolean) {
    this.rig.controls.enabled = !active;
    if (!this.drill.busy) {
      if (active) this.drill.state = 'AUTO_TOUR';
      else
        this.drill.state = this.focusedProjectId
          ? 'PROJECT_FOCUS'
          : this.drill.current.level === 'country'
            ? 'COUNTRY_IDLE'
            : 'REGION_IDLE';
    }
    this.notify();
  }
  private update(dt: number, t: number) {
    this.rig.controls.update(dt);
    this.intro?.update(t);
    this.floor.update(this.reduced ? 0 : t);
    for (const g of this.visibleGroups) {
      const desired = (g.userData.base ?? 0) + (g.userData.lift ?? 0);
      g.position.y += (desired - g.position.y) * Math.min(1, dt * 12);
      const mat = g.userData.top as MeshStandardMaterial | undefined;
      if (mat)
        mat.color.lerp(
          this.hovered === g ? this.hoverColor : this.normalColor,
          Math.min(1, dt * 10)
        );
    }
    for (const g of this.markers.children) {
      if (g.userData.spawn !== undefined && !this.reduced && !this.intro?.active) {
        const p = Math.min(1, (t - g.userData.spawn) / 0.6);
        g.scale.setScalar(1 - Math.pow(1 - p, 3));
      }
    }
    for (const marker of this.markers.children) {
      const projectBeacon = marker.userData.beacon as
        import('../markers/ProjectBeacon').ProjectBeacon | undefined;
      projectBeacon?.update(
        this.reduced ? 0 : t,
        dt,
        marker.userData.hovered,
        this.selectedAsset === (marker.userData.device?.deviceId ?? marker.userData.project?.id),
        marker.userData.crane
      );
    }
    for (const l of this.labels.labels) {
      const code = l.object.userData.countryRegion;
      if (code) {
        l.object.visible = true;
        l.object.userData.forceVisible = this.hovered?.userData.adcode === code;
      }
      if (l.object.userData.hoverCard) l.object.visible = !!l.object.parent?.userData.hovered;
      if (l.object.userData.assetId)
        l.object.userData.selected = l.object.userData.assetId === this.selectedAsset;
    }
    if (!this.reduced) {
      for (const p of this.pulses) p.update(t);
      for (const wave of this.runningWaves) wave.update(t);
      for (const marker of this.markers.children) {
        const beacon = marker.userData.crane?.userData.beacon as
          | import('three').Mesh<
              import('three').CylinderGeometry,
              import('three').MeshBasicMaterial
            >
          | undefined;
        if (beacon && marker.userData.displayStatus === 'RUNNING') {
          const breath = 0.5 + 0.5 * Math.sin(t * 1.35 + (marker.userData.animationPhase ?? 0));
          beacon.material.opacity = 0.45 + 0.55 * breath;
          beacon.scale.setScalar(0.9 + breath * 0.35);
        }
      }

      if (this.network.group.visible) this.network.update(t);
    }
    this.currentTime = t;
    sceneTime.value = this.reduced ? 0 : t;
    this.renderer.info.reset();
    this.post.render(dt);
    this.labels.render(this.scene, this.rig.camera, this.width, this.height);
    this.frames++;
    if (import.meta.env.DEV && t - this.lastStats > 1) {
      this.callbacks.stats({
        'Camera FOV': this.rig.camera.fov,
        'Camera elevation': ((this.rig.elevation * 180) / Math.PI).toFixed(1) + '°',
        'Projected H/W': this.diagnostics().projectedRatio.toFixed(3),
        'Side sample px': this.sideSample().pixels.toFixed(2),
        'Map depth': 0.72,
        'Bevel thickness': 0.055,
        'Active beacons': this.markers.children.filter((g) => g.userData.beacon).length,
        FPS: Math.round(this.frames / (t - this.lastStats)),
        'Draw calls': this.renderer.info.render.calls,
        Triangles: this.renderer.info.render.triangles,
        Textures: this.renderer.info.memory.textures,
        Level: this.drill.current.level,
        Adcode: this.drill.current.adcode,
        Labels: this.labels.labels.length,
        Markers: this.markers.children.length,
        Arcs: this.network.arcCount,
      });
      this.frames = 0;
      this.lastStats = t;
    }
  }
  dispose() {
    this.alive = false;
    this.revision++;
    this.loop.dispose();
    this.intro?.dispose();
    clearTimeout(this.resizeTimer);
    this.observer.disconnect();
    this.interaction.dispose();
    this.rig.dispose();
    this.labels.dispose();
    this.loader.dispose();
    this.post.dispose();
    disposeObject(this.scene);
    this.renderer.dispose();
    this.renderer.forceContextLoss();
    this.renderer.domElement.remove();
  }
}
