import { Mesh, RingGeometry, MeshBasicMaterial } from 'three';
export class RunningWave {
  mesh = new Mesh(
    new RingGeometry(0.67, 0.7, 48),
    new MeshBasicMaterial({ color: 0x65dcb5, transparent: true, opacity: 0.09, depthWrite: false })
  );
  constructor(private size: number) {
    this.mesh.rotation.x = -Math.PI / 2;
    this.mesh.position.y = 0.025;
  }
  update(t: number) {
    const phase = (t / 4.6) % 1;
    this.mesh.scale.setScalar(this.size * (0.7 + phase * 0.8));
    this.mesh.material.opacity = (1 - phase) * 0.13;
  }
}
