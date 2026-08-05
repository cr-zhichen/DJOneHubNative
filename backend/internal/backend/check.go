package backend

// 编译期接口合规性检查（确保所有后端实现都满足 DeviceBackend 接口）
var (
	_ DeviceBackend = (*ATBackend)(nil)
	_ DeviceBackend = (*QMIBackend)(nil)
	_ USSDProvider  = (*ATBackend)(nil)
	_ USSDProvider  = (*QMIBackend)(nil)
)
