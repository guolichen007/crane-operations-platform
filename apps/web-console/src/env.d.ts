import type { MapSceneManager } from './three/map/MapSceneManager';
declare global {
  interface Window {
    __spatialDiagnostics?: () => ReturnType<MapSceneManager['diagnostics']>;
  }
}
export {};
