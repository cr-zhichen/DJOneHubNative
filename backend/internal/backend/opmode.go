package backend

import "context"

// OperatingModeController CFUN / 射频控制接口
type OperatingModeController interface {
	// SetOperatingMode 设置操作模式
	// AT 实现：AT+CFUN=N
	// QMI 实现：DMS.SetOperatingMode
	SetOperatingMode(ctx context.Context, mode OperatingMode) error

	// GetOperatingMode 获取当前操作模式
	// AT 实现：AT+CFUN?
	// QMI 实现：DMS.GetOperatingMode
	GetOperatingMode(ctx context.Context) (OperatingMode, error)

	// Reboot 重启模组
	// AT 实现：AT+CFUN=1,1
	// QMI 实现：DMS.SetOperatingMode(ModeReset)
	Reboot(ctx context.Context) error
}
