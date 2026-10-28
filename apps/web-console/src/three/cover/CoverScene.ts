import { Scene, PerspectiveCamera, GridHelper, FogExp2 } from 'three';
import { createRenderer } from '../core/Renderer';
import { CraneTwinFactory } from '../twin/CraneTwinFactory';
import { addAtmosphere } from '../effects/SceneAtmosphere';
import { SceneLoop } from '../core/SceneLoop';
import { disposeObject } from '../utils/dispose';
export function createCoverScene(host: HTMLElement) {
  const renderer = createRenderer(), scene = new Scene(), camera = new PerspectiveCamera(36, 1, .1, 200);
  renderer.setClearColor(0x06152d, 0); renderer.shadowMap.enabled = false;
  host.append(renderer.domElement);
  scene.fog = new FogExp2(0x06152d, .028);
  addAtmosphere(scene);
  const grid = new GridHelper(70, 70, 0x236bb6, 0x102d52); grid.position.y = -.04; scene.add(grid);
  for (let i = 0; i < 2; i++) {
    const twin = new CraneTwinFactory();
    twin.root.scale.setScalar(.65); twin.root.position.set(7 + i * 4, 0, -i * 13);
    scene.add(twin.root);
  }
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const resize = () => { const w = host.clientWidth, h = host.clientHeight; if (!w || !h) return; renderer.setSize(w, h); camera.aspect = w / h; camera.updateProjectionMatrix(); };
  const observer = new ResizeObserver(resize); observer.observe(host); resize();
  const loop = new SceneLoop((_dt, t) => { camera.position.set(21 + (reduced ? 0 : Math.sin(t * .06) * .6), 13, 24); camera.lookAt(-2, 2, -2); renderer.render(scene, camera); });
  return () => { loop.dispose(); observer.disconnect(); disposeObject(scene); renderer.dispose(); renderer.forceContextLoss(); renderer.domElement.remove(); };
}
