import { Mesh, PlaneGeometry, ShaderMaterial } from 'three';
import { sceneTime } from '../materials/MapSideMaterial';
// Authored screen-space atmosphere, two halos and slowly moving broad light bands.
export class PremiumSceneBackground {
  mesh = new Mesh(
    new PlaneGeometry(2, 2),
    new ShaderMaterial({
      uniforms: { uSceneTime: sceneTime },
      depthWrite: false,
      depthTest: false,
      toneMapped: false,
      vertexShader: `varying vec2 vUv;void main(){vUv=uv;gl_Position=vec4(position.xy,0.9999,1.0);}`,
      fragmentShader: `varying vec2 vUv;uniform float uSceneTime;
    void main(){
      vec2 p=vUv;
      vec2 q=(p-vec2(.52,.46))*vec2(1.6,1.4);
      float center=exp(-dot(q,q)*2.8);
      vec3 c=mix(vec3(.018,.035,.075),vec3(.028,.075,.17),center);
      c=mix(c,vec3(.0431,.1137,.149),smoothstep(.68,1.,p.y)*.45);
      vec2 e=(p-vec2(.68,.52))*vec2(3.,2.3),s=(p-vec2(.38,.3))*vec2(3.5,3.);
      c=mix(c,vec3(.251,.557,.604),exp(-dot(e,e))*.10);
      c=mix(c,vec3(.149,.369,.424),exp(-dot(s,s))*.055);
      float band=exp(-pow((p.x*.65+p.y-.7-.18*sin(uSceneTime*6.283185/29.))/.32,2.));
      float band2=exp(-pow((p.x-p.y*.55-.2-.15*sin(uSceneTime*6.283185/35.+2.))/.36,2.));
      c*=1.+.025*band+.012*band2;
      float noise=fract(sin(dot(gl_FragCoord.xy,vec2(12.9898,78.233)))*43758.5453)-.5;
      gl_FragColor=vec4(c+noise/255.,1.);
    }`,
    })
  );
  constructor() {
    this.mesh.name = 'premium-atmosphere-background';
    this.mesh.frustumCulled = false;
    this.mesh.renderOrder = -1000;
  }
}
