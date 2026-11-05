package equipment

// TypeInfo is one entry of the code-owned equipment type registry: the stable
// type_code, its Chinese display name and its domain (data model §6).
type TypeInfo struct {
	Code   string
	NameZh string
	Domain string
}

// registry is the single code-owned list of equipment types. Every entry has a
// spec schema (specFileFor) and every specFileFor code is listed here; names are
// unique so two different codes are never shown under the same label. The Chinese
// names are standard industrial terms pending OWNER business confirmation; the UI
// always shows the stable code next to the name.
var registry = []TypeInfo{
	// MECHANICAL
	{"MOTOR", "电机", "MECHANICAL"},
	{"REDUCER", "减速机", "MECHANICAL"},
	{"BRAKE", "制动器", "MECHANICAL"},
	{"DRUM", "卷筒", "MECHANICAL"},
	{"HOOK", "吊钩", "MECHANICAL"},
	{"GRAB", "抓斗", "MECHANICAL"},
	{"WHEEL", "车轮", "MECHANICAL"},
	{"COUPLING", "联轴器", "MECHANICAL"},
	// CONTROL
	{"PLC", "PLC 控制器", "CONTROL"},
	{"PLC_IO", "PLC I/O 模块", "CONTROL"},
	{"SAFETY_PLC", "安全 PLC", "CONTROL"},
	{"VFD", "变频器", "CONTROL"},
	{"SERVO_DRIVE", "伺服驱动器", "CONTROL"},
	{"RELAY", "继电器", "CONTROL"},
	{"CONTACTOR", "接触器", "CONTROL"},
	{"REMOTE_CONTROLLER", "遥控器", "CONTROL"},
	// PERCEPTION
	{"LIDAR_3D", "三维激光雷达", "PERCEPTION"},
	{"LIDAR_2D", "二维激光雷达", "PERCEPTION"},
	{"CAMERA_RGB", "彩色相机", "PERCEPTION"},
	{"CAMERA_DEPTH", "深度相机", "PERCEPTION"},
	{"CAMERA_STEREO", "双目相机", "PERCEPTION"},
	{"CAMERA_THERMAL", "热成像相机", "PERCEPTION"},
	{"ENCODER", "编码器", "PERCEPTION"},
	{"ROPE_ENCODER", "拉绳编码器", "PERCEPTION"},
	{"LOAD_CELL", "称重传感器", "PERCEPTION"},
	{"IMU", "惯性测量单元", "PERCEPTION"},
	{"LIMIT_SWITCH", "限位开关", "PERCEPTION"},
	{"PROXIMITY_SENSOR", "接近传感器", "PERCEPTION"},
	{"ANTI_COLLISION_SENSOR", "防撞传感器", "PERCEPTION"},
	{"WIND_SENSOR", "风速传感器", "PERCEPTION"},
	{"TEMPERATURE_SENSOR", "温度传感器", "PERCEPTION"},
	{"VIBRATION_SENSOR", "振动传感器", "PERCEPTION"},
	// COMPUTE / NETWORK / POWER
	{"INDUSTRIAL_PC", "工控机", "COMPUTE"},
	{"EDGE_GATEWAY", "边缘网关", "COMPUTE"},
	{"GPU", "GPU 计算单元", "COMPUTE"},
	{"INDUSTRIAL_SWITCH", "工业交换机", "NETWORK"},
	{"POE_SWITCH", "PoE 交换机", "NETWORK"},
	{"ROUTER", "路由器", "NETWORK"},
	{"MEDIA_CONVERTER", "光电转换器", "NETWORK"},
	{"POWER_SUPPLY", "电源", "POWER"},
	{"UPS", "不间断电源", "POWER"},
	{"TRANSFORMER", "变压器", "POWER"},
	{"CONDUCTOR_RAIL", "滑触线", "POWER"},
}

// Types returns a copy of the registry in its fixed order.
func Types() []TypeInfo {
	return append([]TypeInfo(nil), registry...)
}
