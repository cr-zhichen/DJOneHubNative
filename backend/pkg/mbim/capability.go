package mbim

import "sync/atomic"

type Capabilities struct {
	Services      DeviceServices
	MBIMExOK      bool
	QMIOverMBIMOK bool
	UICCReadOK    bool // READ_BINARY(透明 EF,如 EF_ICCID)
	UICCRecordOK  bool // READ_RECORD(线性记录 EF,如 EF_DIR——AID 解析关键路径)
	UICCChannelOK bool
	AppListOK     bool
	authAKADead   atomic.Bool
}

func (c *Capabilities) AuthAKAUsable() bool {
	if c == nil {
		return false
	}
	return c.Services.HasService(UUIDAuth) && !c.authAKADead.Load()
}

func (c *Capabilities) MarkAuthAKADead() {
	if c != nil {
		c.authAKADead.Store(true)
	}
}

func (c *Capabilities) UICCChannelAKAUsable() bool {
	return c != nil && c.UICCChannelOK
}

func (c *Capabilities) MBIMExUsable() bool {
	return c != nil && c.MBIMExOK
}

func (c *Capabilities) QMIReadUsable() bool {
	return c != nil && c.QMIOverMBIMOK
}

func (c *Capabilities) DeviceResetUsable() bool {
	return c != nil && c.Services.Supports(UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtDeviceReset)
}

// AppListKnownUnsupported 报告 APPLICATION_LIST 是否"确知不支持":仅当 UICC 服务
// 已宣告(init 探针确实跑过)但探针失败时为真。此时 AID 解析应直接走 EF_DIR 直读,
// 跳过注定失败的 APPLICATION_LIST。未宣告/未探(unknown)时返回 false,保留原有
// "先试再回退"行为,不引入回归。
func (c *Capabilities) AppListKnownUnsupported() bool {
	return c != nil && c.Services.HasService(UUIDMSUICCLowLevelAccess) && !c.AppListOK
}
