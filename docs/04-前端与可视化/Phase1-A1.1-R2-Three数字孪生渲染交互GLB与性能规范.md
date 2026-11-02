# Phase1-A1.1-R2 Three.js 数字孪生、渲染交互、GLB 与性能规范

## 1. 当前底座与升级方向

现有 CraneTwinFactory 已有 runway/gantry/trolley/hoist/grab、InstancedMesh、QualityManager、postprocessing、dispose。R2 不推翻，而是加入语义、真实 Equipment、模型版本、传感器和历史时间上下文。

从：`会动的通用行车模型`

升级为：`Physical Semantic Twin`。

## 2. Semantic mapping

```text
Object3D / GLB Node
    ↓ semantic_slot
EquipmentInstallation (current or valid@T)
    ↓ RuntimeBinding
Telemetry / Condition / Alarm
    ↓
Inspector / WorkOrder / History / Documents
```

示例 slot：GANTRY_MOTOR_FL、TROLLEY_MOTOR、HOIST_VFD、HOIST_BRAKE、DRUM、WIRE_ROPE_1、PLC、INDUSTRIAL_PC、LIDAR_FRONT、CAMERA_FRONT。

GLB node 禁止直接写数据库 installation ID；复用模型只保存 semantic slot。

## 3. Twin accuracy

- GENERIC：通用示意；
- PARAMETRIC：按跨度/高度等参数调整；
- CATALOG：对应某产品/系列模型；
- AS_BUILT：来自实际 CAD/测量并完成版本/坐标确认。

UI 必须显示准确度，禁止 generic 冒充现场真实尺寸。

## 4. Rendering

生产使用 WebGL；不把 WebGPU 实验风险塞入 R2。

规范：PBR/standard materials、ACES tone mapping、environment/IBL、Directional key + Hemisphere、soft shadow、subtle floor/grid、轻雾按场景选用。

### 状态视觉

- selected：outline + 局部亮度；
- warning：amber outline/indicator；
- alarm：局部 red emissive + 低频 pulse + selective bloom；
- offline/unknown：降低饱和度/灰色标签，不把整车变灰遮蔽结构。

禁止满屏 bloom、持续粒子和无业务意义霓虹。

## 5. 交互工具

必须：Select、Focus、Isolate、Hide、X-Ray、Reset、Overview/Top/Side/Hoist Camera、Equipment Tree、Scene Search、Alarm Focus、Sensor FOV、Coordinate Frame、Labels、Measurement。

可选：Section/Clip，只在 B7 真实需求和性能允许时加入。

### 选择反馈

click 后 <100ms 目标给 selection feedback；Inspector 可先 skeleton，数据请求异步，不冻结画面。

## 6. Raycast 与 Label 性能

Raycaster 只在 pointer move/click 运行并 throttle；不每帧全场景 raycast。HTML label 默认仅 selected/hover/alarm；大量标签使用 WebGL sprite/批量策略或按需显示。

## 7. Camera motion

Camera transition 可中断；新 Focus 必须 cancel/replace 旧 tween。GSAP 不在 render loop 每帧创建 tween。

## 8. Sensor Visualization

### LiDAR

显示设备 body、mount pose、local coordinate frame、horizontal/vertical FOV、range volume。CALIBRATED/SURVEY 才标“精确安装”；MANUAL/CAD 未验证时显示“示意/设计位姿”。

### Camera

显示 frustum、optical axis、mount frame；可切换 visibility。

### 数据安全

FOV 与 pose 属业务/运维敏感信息；公共 demo 只用 synthetic 数据。

## 9. System Topology

不要把网络/控制线全塞 Three。Topology 是独立 2D workspace，数据来自 equipment_relations。

视图：Control、Network、Power、Sensor。例：PLC→VFD→Motor；IPC→Switch→LiDAR/Camera。

Vue Flow 仅 PoC；若 bundle/交互不值，引导自研 SVG。

## 10. GLB Pipeline

```text
CAD/Blender
  → clean hierarchy / pivot / units
  → semantic node names
  → GLB
  → Meshopt (优先)
  → KTX2 textures
  → Draco（仅实测收益后）
  → validation
  → model artifact + manifest
```

Three GLTFLoader 支持 Meshopt/KTX2/Draco。Decoder 本地托管，符合 CSP，不从第三方 CDN 运行时拉取。

## 11. twin_models

建议：

```text
id
asset_type/catalog_ref*
version
sha256
accuracy_level
units
coordinate_frame
source
semantic_manifest_version
artifact_document_id*
status
valid_from
valid_to
```

历史 Replay 使用 `model valid at T`，不能永远拿今天模型解释过去。

## 12. Semantic Manifest

示例：

```json
{
  "version": 1,
  "slots": {
    "HOIST_VFD": {"nodes": ["VFD_HOIST"], "selectable": true},
    "LIDAR_FRONT": {"nodes": ["SENSOR_LIDAR_F"], "selectable": true, "sensor": "lidar"}
  }
}
```

构建/测试验证 node 存在、slot 不重复、单位/坐标系明确。

## 13. RuntimeFrameAdapter

High-frequency stream → latest frame buffer。rAF 每帧读取最新有效 frame，按时间做有限插值；UI/Charts 降频。

Replay 与 LIVE 使用同一 `TwinFrame` 接口，但不同 source：
- `LiveFrameSource`；
- `ReplayFrameSource`。

进入 HISTORY 必须断开对画面的 LIVE 驱动，底层连接可保持但不能覆盖 replay。

## 14. Performance Gate

目标基线（B7/B10 实测）：

- Premium 50~60 FPS，DPR≤1.5；
- Balanced ≥40 FPS；
- Reduced ≥30 FPS；
- draw calls <350；
- triangles <700k（Premium 目标）；
- click→selection <100ms target；
- Inspector skeleton <100ms target；
- Long Task >50ms 不持续出现。

模型超预算：优先 InstancedMesh、LOD、Meshopt、KTX2、culling、减少 transparency/postprocess，而不是直接降低功能正确性。

## 15. Resource lifecycle

GLTF textures/materials/geometries、postprocess targets、controls、ResizeObserver、RAF/SceneLoop、event listeners 全部必须 dispose。Playwright/diagnostics 做 20 次 route cycles，renderer memory 不持续增长、无 duplicate canvas。

## 16. WebGL fallback

WebGL unavailable 时 Twin 显示清晰 fallback：设备档案、状态、拓扑和历史仍可用。3D 不是业务唯一入口。

