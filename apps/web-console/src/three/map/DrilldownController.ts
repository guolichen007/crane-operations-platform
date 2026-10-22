export type MapLevel = {
  adcode: number;
  name: string;
  level: 'country' | 'province' | 'prefecture' | 'county';
};
export type SceneState =
  | 'COUNTRY_IDLE'
  | 'REGION_HOVER'
  | 'DRILLING_IN'
  | 'REGION_IDLE'
  | 'PROJECT_FOCUS'
  | 'DRILLING_OUT'
  | 'AUTO_TOUR';
export const countryLevel: MapLevel = { adcode: 100000, name: '全国', level: 'country' };
export class DrilldownController {
  stack: MapLevel[] = [countryLevel];
  state: SceneState = 'COUNTRY_IDLE';
  get current() {
    return this.stack[this.stack.length - 1]!;
  }
  get busy() {
    return this.state === 'DRILLING_IN' || this.state === 'DRILLING_OUT';
  }
  push(level: MapLevel) {
    if (this.stack.some((l) => l.adcode === level.adcode)) return;
    this.stack.push(level);
  }
  back(index: number) {
    this.stack = this.stack.slice(0, index + 1);
    return this.current;
  }
}
