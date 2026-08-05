package backend

import "context"

// SMSCProvider 可选能力接口：读取短信中心号码（SMSC）。
// 该接口不并入 DeviceBackend 聚合，调用方按需进行类型断言。
type SMSCProvider interface {
	GetSMSC(ctx context.Context) (string, error)
}
