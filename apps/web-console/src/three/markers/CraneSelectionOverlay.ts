import { Group, Mesh, RingGeometry, MeshBasicMaterial, CylinderGeometry, DoubleSide } from 'three';

/**
 * CraneSelectionOverlay is a single reusable cyan ring + stem for the selected
 * and hovered crane glyph. It never mutates the InstancedMesh; it is moved to
 * the glyph position and shown/hidden, so selection never repaints the fleet.
 */
export class CraneSelectionOverlay {
  group = new Group();
  private ring: Mesh<RingGeometry, MeshBasicMaterial>;
  private stem: Mesh<CylinderGeometry, MeshBasicMaterial>;
  private selected = false;
  private hovered = false;

  constructor(scale = 1) {
    this.ring = new Mesh(
      new RingGeometry(0.62, 0.7, 32),
      new MeshBasicMaterial({
        color: '#38e1ff',
        transparent: true,
        opacity: 0.95,
        depthWrite: false,
        side: DoubleSide,
        toneMapped: false,
      })
    );
    this.ring.rotation.x = -Math.PI / 2;
    this.ring.position.y = 0.02;
    this.stem = new Mesh(
      new CylinderGeometry(0.045, 0.045, 1.7, 8),
      new MeshBasicMaterial({
        color: '#38e1ff',
        transparent: true,
        opacity: 0.4,
        depthWrite: false,
        toneMapped: false,
      })
    );
    this.stem.position.y = 0.85;
    this.group.add(this.ring, this.stem);
    this.group.scale.setScalar(scale);
    this.group.visible = false;
  }

  set(position: [number, number, number] | null, selected: boolean, hovered: boolean) {
    this.selected = selected;
    this.hovered = hovered;
    if (!position) {
      this.group.visible = false;
      return;
    }
    this.group.visible = true;
    this.group.position.set(position[0], position[1], position[2]);
  }

  update(t: number, reduced: boolean) {
    if (!this.group.visible || reduced) return;
    const pulse = this.selected ? 0.6 + 0.4 * Math.sin(t * 2.4) : 0.5;
    this.ring.material.opacity = (this.selected ? 0.95 : 0.7) * pulse;
    this.stem.material.opacity = this.selected ? 0.4 : 0.25;
  }

  dispose() {
    this.group.removeFromParent();
    this.ring.geometry.dispose();
    this.stem.geometry.dispose();
    this.ring.material.dispose();
    this.stem.material.dispose();
  }
}
