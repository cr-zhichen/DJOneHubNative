package backend

import "context"

// DiscoveryProvider 设备发现接口
type DiscoveryProvider interface {
	// ProbeIMEI 通过指定端口探测设备 IMEI
	// AT 实现：打开 ttyUSB → AT+GSN
	// QMI 实现：打开 cdc-wdm → DMS.GetDeviceSerialNumbers
	ProbeIMEI(ctx context.Context, port string) (string, error)
}
