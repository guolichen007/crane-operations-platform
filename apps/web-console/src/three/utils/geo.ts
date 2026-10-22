import type { Feature } from 'geojson';
export const featureCode = (f: Feature) => Number(f.properties?.adcode);
export const featureName = (f: Feature) => String(f.properties?.name ?? '');
