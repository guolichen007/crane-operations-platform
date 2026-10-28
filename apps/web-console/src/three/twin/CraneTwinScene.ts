import { Scene, GridHelper, FogExp2, Mesh, PlaneGeometry, MeshStandardMaterial, Box3, Vector3 } from 'three';
import { createRenderer } from '../core/Renderer';
import { CameraRig } from '../core/CameraRig';
import { SceneLoop } from '../core/SceneLoop';
import { QualityManager } from '../core/QualityManager';
import { createPostProcessing } from '../effects/PostProcessing';
import { addAtmosphere } from '../effects/SceneAtmosphere';
import { disposeObject } from '../utils/dispose';
import { CraneTwinFactory, type TwinAxes } from './CraneTwinFactory';
export class CraneTwinScene {
  private scene = new Scene(); private renderer = createRenderer(); private rig: CameraRig;
  readonly twin = new CraneTwinFactory(); private loop: SceneLoop; private observer: ResizeObserver;
  private quality = new QualityManager(devicePixelRatio, matchMedia('(prefers-reduced-motion: reduce)').matches, this.renderer.capabilities.maxTextureSize);
  private post: ReturnType<typeof createPostProcessing>;
  constructor(private host: HTMLElement, private demo = false) {
    host.append(this.renderer.domElement); this.renderer.setClearColor(0x06152d); this.renderer.setPixelRatio(this.quality.pixelRatio);
    this.rig = new CameraRig(this.renderer.domElement); this.rig.controls.minDistance = 8; this.rig.controls.maxDistance = 90;
    this.rig.controls.setLookAt(27, 20, 27, 0, 4, 0, false);
    this.scene.fog = new FogExp2(0x06152d, .012); addAtmosphere(this.scene); this.scene.add(this.twin.root);
    const floor = new Mesh(new PlaneGeometry(120, 120), new MeshStandardMaterial({ color: '#091725', roughness: .8, metalness: .25 })); floor.rotation.x = -Math.PI / 2; floor.position.y = -.3; floor.receiveShadow = true; this.scene.add(floor, new GridHelper(100, 100, 0x23476b, 0x122a42));
    this.post = createPostProcessing(this.renderer, this.scene, this.rig.camera); this.post.setQuality(this.quality.tier); this.twin.setQuality(this.quality.tier);
    this.observer = new ResizeObserver(this.resize); this.observer.observe(host); this.resize();
    this.loop = new SceneLoop((dt, t) => {
      if (this.demo && !this.quality.reducedMotion) this.twin.setAxes({ x: Math.sin(t * .11) * 4, y: Math.cos(t * .14) * 2, z: 4 + Math.sin(t * .18) });
      if (this.quality.sample(dt)) { this.renderer.setPixelRatio(this.quality.pixelRatio); this.post.setQuality(this.quality.tier); this.twin.setQuality(this.quality.tier); this.resize(); }
      this.rig.controls.update(dt); this.renderer.info.reset(); this.post.render(dt);
    });
  }
  private resize = () => { const w = this.host.clientWidth, h = this.host.clientHeight; if (!w || !h) return; this.renderer.setSize(w, h); this.post.setSize(w, h); this.rig.resize(w, h); };
  setAxes(axes: Partial<TwinAxes>) { this.twin.setAxes(axes); }
  setHealth(state: 'normal' | 'alarm' | 'unknown') { this.twin.setHealth(state); }
  setSelection(selected: boolean) { this.twin.setSelection(selected); const meshes: Mesh[] = []; this.twin.root.traverse(o => { if (o instanceof Mesh && selected) meshes.push(o); }); this.post.setHighlights({ selected: meshes }); }
  frame(inspectorWidth = 0) { this.rig.setViewPadding(inspectorWidth); return this.rig.frame(new Box3(new Vector3(-15, 0, -7), new Vector3(15, 11, 7)), 'PROJECT_FOCUS', !this.quality.reducedMotion); }
  diagnostics() { return { quality: this.quality.tier, drawCalls: this.renderer.info.render.calls, triangles: this.renderer.info.render.triangles, memory: { ...this.renderer.info.memory } }; }
  dispose() { this.loop.dispose(); this.observer.disconnect(); this.rig.dispose(); this.post.dispose(); disposeObject(this.scene); this.renderer.dispose(); this.renderer.forceContextLoss(); this.renderer.domElement.remove(); }
}
