import {
  Group,
  PlaneGeometry,
  Mesh,
  ShaderMaterial,
  BufferGeometry,
  Float32BufferAttribute,
  Points,
  PointsMaterial,
  AdditiveBlending,
  Vector2,
} from 'three';
import { sceneTime } from '../materials/MapSideMaterial';
export class SpatialFloor {
  group = new Group();
  private focus = { value: new Vector2(9, 8) };
  private points: Points;
  constructor() {
    this.group.name = 'spatial-grid-sweep-points';
    const material = new ShaderMaterial({
      transparent: true,
      depthWrite: false,
      blending: AdditiveBlending,
      uniforms: { uSceneTime: sceneTime, uFocus: this.focus },
      vertexShader: `varying vec2 vGround;void main(){vGround=position.xy;gl_Position=projectionMatrix*modelViewMatrix*vec4(position,1.0);}`,
      fragmentShader: `varying vec2 vGround;uniform float uSceneTime;uniform vec2 uFocus;
      void main(){
        vec2 cell=abs(fract((vGround+1.5)/3.0)-0.5)/fwidth(vGround/3.0);
        float grid=1.0-min(min(cell.x,cell.y),1.0);
        float fade=1.0-smoothstep(35.0,72.0,length(vGround*vec2(0.8,1.0)));
        float dist=length(vGround-uFocus),radius=mod(uSceneTime/11.0,1.0)*78.0;
        float sweep=(1.0-smoothstep(0.0,0.7,abs(dist-radius)))*(1.0-radius/78.0);
        float periphery=smoothstep(25.0,46.0,length(vGround*vec2(0.8,1.0)));
        float alpha=fade*periphery*(grid*0.022+sweep*0.035);
        gl_FragColor=vec4(0.20,0.52,0.60,alpha);
      }`,
    });
    const grid = new Mesh(new PlaneGeometry(140, 140), material);
    grid.rotation.x = -Math.PI / 2;
    grid.position.y = -0.6;
    const positions: number[] = [];
    for (let i = 0; i < 84; i++) {
      const a = i * 2.399963,
        b = 35 + ((i * 37) % 59);
      positions.push(Math.cos(a) * b, -0.35 + Math.sin(i * 1.7) * 0.18, Math.sin(a) * b * 0.75);
    }
    const geo = new BufferGeometry();
    geo.setAttribute('position', new Float32BufferAttribute(positions, 3));
    this.points = new Points(
      geo,
      new PointsMaterial({
        color: 0x75c6d3,
        size: 0.052,
        transparent: true,
        opacity: 0.13,
        depthWrite: false,
      })
    );
    this.group.add(grid, this.points);
  }
  setFocus(x: number, z: number) {
    this.focus.value.set(x, -z);
  }
  update(t: number) {
    this.points.position.y = Math.sin(t * 0.16) * 0.035;
  }
}
