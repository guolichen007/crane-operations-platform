import { geoMercator } from 'd3-geo';
const projection = geoMercator().center([104, 35]).scale(82).translate([0, 0]);
export function project(lng: number, lat: number): [number, number] {
  return projection([lng, lat]) as [number, number];
}

// The overview uses a labelled South China Sea inset. Detail views keep geographic coordinates.
export function projectCountry(lng: number, lat: number): [number, number] {
  return lat < 17.6 && lng > 105
    ? project(124 + (lng - 115) * 0.16, 20 + (lat - 10) * 0.3)
    : project(lng, lat);
}
