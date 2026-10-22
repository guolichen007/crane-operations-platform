export type CameraPresetName =
  | 'COUNTRY_OVERVIEW'
  | 'COUNTRY_CINEMATIC'
  | 'PROVINCE_CONTEXT'
  | 'CITY_CONTEXT'
  | 'PROJECT_FOCUS'
  | 'DEVICE_FOCUS'
  | 'PROVINCE_CINEMATIC'
  | 'CITY_CINEMATIC'
  | 'PROJECT_CINEMATIC'
  | 'DEVICE_CINEMATIC';
export type CameraPreset = {
  width: number;
  height: number;
  elevation: number;
  fov: number;
  yaw: number;
};
export const CameraPresets: Record<CameraPresetName, CameraPreset> = {
  COUNTRY_OVERVIEW: { width: 0.62, height: 0.88, elevation: 0.72, fov: 38, yaw: 0.1 },
  COUNTRY_CINEMATIC: { width: 0.62, height: 0.88, elevation: 0.65, fov: 40, yaw: 0.13 },
  PROVINCE_CONTEXT: { width: 0.52, height: 0.92, elevation: 0.82, fov: 38, yaw: 0.1 },
  CITY_CONTEXT: { width: 0.46, height: 0.82, elevation: 0.88, fov: 38, yaw: 0.08 },
  PROJECT_FOCUS: { width: 0.52, height: 0.82, elevation: 0.68, fov: 38, yaw: 0.1 },
  DEVICE_FOCUS: { width: 0.44, height: 0.24, elevation: 0.72, fov: 38, yaw: 0.1 },
  PROVINCE_CINEMATIC: { width: 0.52, height: 0.86, elevation: 0.72, fov: 40, yaw: 0.13 },
  CITY_CINEMATIC: { width: 0.46, height: 0.82, elevation: 0.78, fov: 40, yaw: 0.13 },
  PROJECT_CINEMATIC: { width: 0.52, height: 0.82, elevation: 0.6, fov: 40, yaw: 0.13 },
  DEVICE_CINEMATIC: { width: 0.44, height: 0.24, elevation: 0.62, fov: 40, yaw: 0.13 },
};
