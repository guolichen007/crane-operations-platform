# Third-party notices

## ThreeMaps — visual reference only

- Project: ThreeMaps
- Source: https://github.com/wangscript007/ThreeMaps
- Reviewed revision: df24dee8816425812d1164d44cbfa73af7269e93
- Repository license: Apache-2.0; text retained in licenses/ThreeMaps-Apache-2.0.txt for reference.
- Reviewed files: src/views/gdMap/map.js, map.vue, assets.js; src/mini3d/components/ExtrudeMap.js, BaseMap.js; src/mini3d/core/Camera.js.
- Copied/modified upstream production files: **none**. Upstream code, textures, map data, UI and mini3d are not included in this project or its release. Visual mechanisms were independently implemented for the existing application.
- File-level review found no additional notices in those six files; this statement does not assert rights in unreviewed assets.

## GSAP 3.15.0 — runtime dependency

Copyright 2008-2026, GreenSock. All rights reserved. Author: Jack Doyle.
Subject to the Standard “No Charge” GSAP License: https://gsap.com/standard-license . This is not an MIT license. The installed package banner is retained in licenses/GSAP-3.15.0-NOTICE.txt; its proprietary notice is preserved with the production bundle.

Used for a preprogrammed scene entrance sequence, driven from the scene clock. No visual animation authoring/builder tool is provided.

## Existing runtime dependencies

Vue, Pinia, Vue Router, Three.js, camera-controls, postprocessing, d3-geo, ECharts and lucide-vue-next remain npm runtime dependencies. Available package license texts are retained under licenses/. Exact resolved versions are recorded in package-lock.json. Map source and internal-prototype limitations remain documented in apps/web-console/public/geo/source.json and docs/04-前端与可视化/地图数据合规说明.md.


## Go runtime dependencies

The cloud/edge services use Eclipse Paho Go (EPL-2.0 and EDL-1.0), pgx (MIT), Goose (MIT), and coder/websocket (ISC). Exact versions and transitive modules are locked in go.mod/go.sum. Upstream license files are available in the Go module distribution; packaging for production must retain the applicable notices. No root license for this commercial project is selected.

Inventory and Fleet use go.yaml.in/yaml/v3 v3.0.5 with strict known-field decoding. Its original MIT / Apache notices are retained verbatim in licenses/go-yaml-v3-LICENSE.txt. Package documentation: https://pkg.go.dev/go.yaml.in/yaml/v3 . This dependency does not change the project's root licensing decision.
