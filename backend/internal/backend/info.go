package backend

import "context"

// DeviceInfoProvider 设备信息查询接口（状态查询主线）
type DeviceInfoProvider interface {
	// GetIMEI 获取设备 IMEI
	// AT 实现：AT+CGSN
	// QMI 实现：DMS.GetDeviceSerialNumbers
	GetIMEI(ctx context.Context) (string, error)

	// GetIMSI 获取 SIM 卡 IMSI
	// AT 实现：AT+CIMI
	// QMI 实现：UIM.GetIMSI
	GetIMSI(ctx context.Context) (string, error)

	// GetICCID 获取 SIM 卡 ICCID
	// AT 实现：AT+QCCID
	// QMI 实现：UIM.GetICCID
	GetICCID(ctx context.Context) (string, error)

	// GetMSISDN 获取本机号码。
	// AT 实现：AT+CNUM
	// QMI 实现：DMS.GetMSISDN
	GetMSISDN(ctx context.Context) (string, error)

	// GetRevision 获取固件版本
	// AT 实现：AT+CGMR
	// QMI 实现：DMS.GetDeviceRevision
	GetRevision(ctx context.Context) (string, error)

	// GetSignalInfo 获取信号质量信息
	// AT 实现：AT+CSQ + AT+QENG="servingcell"
	// QMI 实现：NAS.GetSignalStrength + NAS.GetSignalInfo
	GetSignalInfo(ctx context.Context) (*SignalInfo, error)

	// GetServingSystem 获取网络注册状态和服务系统信息
	// AT 实现：AT+CREG? + AT+COPS?
	// QMI 实现：NAS.GetServingSystem
	GetServingSystem(ctx context.Context) (*ServingSystem, error)

	// IsSimInserted 判断 SIM 卡是否已插入
	// AT 实现：AT+QSIMSTAT? / AT+CPIN?
	// QMI 实现：DMS.GetSIMStatus
	IsSimInserted(ctx context.Context) (bool, error)

	// GetNativeMCCMNC 获取 SIM 归属 MCC 和 MNC（区别于当前驻留网络）。
	// 实现必须基于 IMSI + EF_AD
	GetNativeMCCMNC(ctx context.Context) (mcc string, mnc string, err error)

	// GetNativeSPN 读取 SIM EF_SPN 服务提供商名称（区别于当前驻留网络 operator）。
	// AT 实现：AT+CRSM 读取 EF_SPN
	// QMI 实现：UIM.ReadTransparent读取 EF_SPN
	GetNativeSPN(ctx context.Context) (string, error)

	// GetSIMMetadata 读取 SIM/eSIM profile 的原生元数据。
	// AT 实现：AT+CRSM 读取 EF_AD/GID/PNN/OPL/SST/UST
	// QMI 实现：UIM.ReadTransparent/ReadRecord 读取对应 EF
	GetSIMMetadata(ctx context.Context) (*SIMMetadata, error)
}
