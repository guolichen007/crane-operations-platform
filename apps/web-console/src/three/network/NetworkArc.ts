import {
  QuadraticBezierCurve3,
  Vector3,
  BufferGeometry,
  Line,
  LineDashedMaterial,
  Mesh,
  TubeGeometry,
  ShaderMaterial,
  AdditiveBlending,
  MathUtils,
} from 'three';
import { sceneTime } from '../materials/MapSideMaterial';
export class NetworkArc {
  curve: QuadraticBezierCurve3;
  line: Line | Mesh;
  readonly height: number;
  constructor(start: Vector3, end: Vector3, planned = false, phase = 0) {
    const mid = start.clone().lerp(end, 0.5);
    this.height = MathUtils.clamp(start.distanceTo(end) * 0.14, 0.8, 3.2);
    mid.y += this.height;
    this.curve = new QuadraticBezierCurve3(start, mid, end);
    this.line = planned
      ? new Line(
          new BufferGeometry().setFromPoints(this.curve.getPoints(64)),
          new LineDashedMaterial({
            color: 0xd3ae68,
            transparent: true,
            opacity: 0.14,
            dashSize: 0.35,
            gapSize: 0.25,
            depthWrite: false,
          })
        )
      : new Mesh(
          new TubeGeometry(this.curve, 64, 0.025, 4, false),
          new ShaderMaterial({
            uniforms: { uSceneTime: sceneTime, uPhase: { value: phase }, uOpacity: { value: 1 } },
            transparent: true,
            depthWrite: false,
            blending: AdditiveBlending,
            toneMapped: false,
            vertexShader:
              'varying float vProgress;void main(){vProgress=uv.x;gl_Position=projectionMatrix*modelViewMatrix*vec4(position,1.0);}',
            fragmentShader: `varying float vProgress;uniform float uSceneTime;uniform float uPhase;uniform float uOpacity;
          void main(){float p=fract(uSceneTime*0.14+uPhase);float tail=p-vProgress;
          float light=step(0.0,tail)*(1.0-smoothstep(0.0,0.045,tail));
          gl_FragColor=vec4(mix(vec3(0.12,0.48,0.60),vec3(0.72,0.98,1.0),light),(0.08+light*0.57)*uOpacity);}`,
          })
        );
    this.line.onBeforeRender = () => {
      const m = this.line.material;
      if (m instanceof ShaderMaterial) m.uniforms.uOpacity!.value = m.opacity;
    };
    if (this.line instanceof Line) this.line.computeLineDistances();
  }
}
