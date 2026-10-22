import { Group, Vector3 } from 'three';
import { CloudNode } from './CloudNode';
import { NetworkArc } from './NetworkArc';
import { NetworkParticle } from './NetworkParticle';
import { project } from '../map/GeoProjection';
import type { Project } from '../../data/types';
import { disposeObject } from '../utils/dispose';
export class NetworkLayer {
  group = new Group();
  particles: NetworkParticle[] = [];
  private batch = -1;
  private routes = new Group();
  private node = new CloudNode();
  readonly cloud = new Vector3(7, 7, -3);
  arcCount = 0;
  plannedCount = 0;
  routeIds: string[] = [];
  setProjects(items: Project[]) {
    this.projects = items;
    this.batch = -1;
    this.update(0);
  }
  constructor(private projects: Project[]) {
    this.node.group.position.copy(this.cloud);
    this.group.add(this.node.group, this.routes);
  }
  update(t: number) {
    const batch = Math.floor(t / 5.5);
    if (this.batch !== batch) {
      this.batch = batch;
      disposeObject(this.routes);
      this.routes = new Group();
      this.group.add(this.routes);
      this.particles = [];
      this.arcCount = 0;
      this.plannedCount = 0;
      this.routeIds = [];
      const demos = this.projects.filter((p) => p.dataSource === 'demo'),
        planned = this.projects.filter((p) => p.dataSource === 'real');
      const dynamicCount = Math.min(planned.length ? 6 : 8, demos.length);
      const plannedCount = Math.min(planned.length, demos.length ? 4 : 6);
      const selected = [
        ...Array.from({ length: dynamicCount }, (_, i) => demos[(batch * 3 + i) % demos.length]!),
        ...Array.from(
          { length: plannedCount },
          (_, i) => planned[(batch * 2 + i) % planned.length]!
        ),
      ];
      for (const [i, p] of selected.entries()) {
        const [x, z] = project(p.approximateLng, p.approximateLat);
        const isPlanned = p.dataSource === 'real';
        const arc = new NetworkArc(new Vector3(x, 0.7, z), this.cloud, isPlanned, i * 0.17);
        this.routes.add(arc.line);
        this.arcCount++;
        this.routeIds.push(p.id);
        if (isPlanned) this.plannedCount++;
        else {
          const head = new NetworkParticle(arc, i * 0.17);
          this.particles.push(head);
          this.routes.add(head.group);
        }
      }
    }
    for (const p of this.particles) p.update(t);
    this.node.update(t);
  }
  snapshot() {
    return {
      arcCount: this.arcCount,
      plannedCount: this.plannedCount,
      dynamicCount: this.particles.length,
      batch: this.batch,
      routeIds: this.routeIds,
      orbitAngle: this.node.angle,
      heads: this.particles.map((p) => p.mesh.position.toArray()),
    };
  }
}
