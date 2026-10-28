import {
  Scene,
  GridHelper,
  FogExp2,
  Mesh,
  PlaneGeometry,
  MeshBasicMaterial,
  Box3,
  Vector3,
  HemisphereLight,
  DirectionalLight,
  type Object3D,
} from 'three';
import { createRenderer } from '../core/Renderer';
import { CameraRig } from '../core/CameraRig';
import { SceneLoop } from '../core/SceneLoop';
import { QualityManager } from '../core/QualityManager';
import { createPostProcessing } from '../effects/PostProcessing';
import { addAtmosphere } from '../effects/SceneAtmosphere';
import { PremiumSceneBackground } from '../effects/PremiumSceneBackground';
import { disposeObject } from '../utils/dispose';
import { CraneTwinFactory, type TwinAxes } from './CraneTwinFactory';
export class CraneTwinScene {
  private scene = new Scene();
  private renderer = createRenderer();
  private rig: CameraRig;
  readonly twin = new CraneTwinFactory();
  private loop: SceneLoop;
  private observer: ResizeObserver;
  private quality = new QualityManager(
    devicePixelRatio,
    matchMedia('(prefers-reduced-motion: reduce)').matches,
    this.renderer.capabilities.maxTextureSize
  );
  private post: ReturnType<typeof createPostProcessing>;
  constructor(
    private host: HTMLElement,
    private demo = false
  ) {
    host.append(this.renderer.domElement);
    this.renderer.info.autoReset = false;
    this.renderer.setClearColor(0x06152d);
    this.renderer.setPixelRatio(this.quality.pixelRatio);
    this.rig = new CameraRig(this.renderer.domElement);
    this.rig.controls.minDistance = 8;
    this.rig.controls.maxDistance = 90;
    this.rig.controls.setLookAt(23, 16, 21, 1, 4.5, 0, false);
    this.scene.fog = new FogExp2(0x06152d, 0.012);
    addAtmosphere(this.scene);
    this.scene.add(this.twin.root);
    this.scene.add(new PremiumSceneBackground().mesh);
    this.scene.add(new HemisphereLight(0xdceeff, 0x25416a, 1.3));
    const studio = new DirectionalLight(0xffedca, 2);
    studio.position.set(10, 18, 12);
    this.scene.add(studio);
    const floor = new Mesh(
      new PlaneGeometry(500, 500),
      new MeshBasicMaterial({ color: '#102a45' })
    );
    floor.rotation.x = -Math.PI / 2;
    floor.position.y = -0.3;
    const grid = new GridHelper(100, 100, 0x3c709f, 0x1f4568);
    grid.material.transparent = true;
    grid.material.opacity = 0.4;
    this.scene.add(floor, grid);
    this.post = createPostProcessing(this.renderer, this.scene, this.rig.camera);
    this.post.setQuality(this.quality.tier);
    this.twin.setQuality(this.quality.tier);
    const beacons: Object3D[] = [];
    this.twin.root.traverse((object) => {
      if (object.userData.bloom) beacons.push(object);
    });
    this.post.setHighlights({ bloom: beacons });
    this.observer = new ResizeObserver(this.resize);
    this.observer.observe(host);
    this.resize();
    this.loop = new SceneLoop((dt, t) => {
      if (this.demo && !this.quality.reducedMotion)
        this.twin.setAxes({
          x: Math.sin(t * 0.11) * 4,
          y: Math.cos(t * 0.14) * 2,
          z: 4 + Math.sin(t * 0.18),
        });
      if (this.quality.sample(dt)) {
        this.renderer.setPixelRatio(this.quality.pixelRatio);
        this.post.setQuality(this.quality.tier);
        this.twin.setQuality(this.quality.tier);
        this.resize();
      }
      this.rig.controls.update(dt);
      this.renderer.info.reset();
      this.post.render(dt);
    });
  }
  private resize = () => {
    const w = this.host.clientWidth,
      h = this.host.clientHeight;
    if (!w || !h) return;
    this.renderer.setSize(w, h);
    this.post.setSize(w, h);
    this.rig.resize(w, h);
  };
  setAxes(axes: Partial<TwinAxes>) {
    this.twin.setAxes(axes);
  }
  setHealth(state: 'normal' | 'alarm' | 'unknown') {
    this.twin.setHealth(state);
  }
  setSelection(selected: boolean) {
    this.twin.setSelection(selected);
    const meshes: Mesh[] = [];
    this.twin.root.traverse((o) => {
      if (o instanceof Mesh && selected) meshes.push(o);
    });
    this.post.setHighlights({ selected: meshes });
  }
  frame(inspectorWidth = 0) {
    this.rig.setViewPadding(inspectorWidth);
    return this.rig.frame(
      new Box3(new Vector3(-15, 0, -7), new Vector3(15, 11, 7)),
      'PROJECT_FOCUS',
      !this.quality.reducedMotion
    );
  }
  diagnostics() {
    return {
      quality: this.quality.tier,
      drawCalls: this.renderer.info.render.calls,
      triangles: this.renderer.info.render.triangles,
      memory: { ...this.renderer.info.memory },
    };
  }
  dispose() {
    this.loop.dispose();
    this.observer.disconnect();
    this.rig.dispose();
    this.post.dispose();
    disposeObject(this.scene);
    this.renderer.dispose();
    this.renderer.forceContextLoss();
    this.renderer.domElement.remove();
  }
}
