package backend

import "time"

// SignalInfo 信号质量信息（AT 和 QMI 后端统一返回此结构）
type SignalInfo struct {
	// 通用信号强度
	RSSI int // dBm（AT+CSQ 转换值 或 NAS.GetSignalStrength）

	// LTE 专有
	RSRP int // dBm（AT+QENG 或 NAS.GetSignalInfo）
	RSRQ int // dB（AT+QENG 或 NAS.GetSignalInfo）
	SINR int // dB（NAS.GetSignalInfo LTE RSSNR）

	// 5G 专有（NAS.GetSignalInfo 5G TLV）
	NR5GRSRP int
	NR5GRSRQ int
	NR5GSINR int
}

// ServingSystem 网络注册状态（AT 和 QMI 后端统一返回此结构）
type ServingSystem struct {
	// 注册状态（0=未注册, 1=本地注册, 2=搜索中, 3=被拒, 4=未知, 5=漫游注册）
	RegStatus     int
	RegStatusText string

	// PLMN 信息
	Operator string // 运营商名称/代码
	MCC      uint16
	MNC      uint16

	// 位置信息
	LAC    string // 位置区代码
	CellID string // 小区 ID

	// 接入技术
	NetworkMode   string // LTE/WCDMA/GSM 等
	NetworkDuplex string // FDD/TDD
	RadioBand     string // 当前服务小区/无线接口频段
	RadioChannel  uint32 // EARFCN/ARFCN/channel

	// PS 附着状态
	PSAttached bool
}

// SIMMetadata 表示 SIM/eSIM profile 的原生元数据。
type SIMMetadata struct {
	NativeMCC    string
	NativeMNC    string
	GID1         string
	GID2         string
	PNN          []PNNRecord
	OPL          []OPLRecord
	ServiceTable *SIMServiceTable
}

type PNNRecord struct {
	Record    int    `json:"record"`
	FullName  string `json:"full_name,omitempty"`
	ShortName string `json:"short_name,omitempty"`
	RawHex    string `json:"raw_hex,omitempty"`
}

type OPLRecord struct {
	Record    int    `json:"record"`
	PLMN      string `json:"plmn,omitempty"`
	LACStart  uint16 `json:"lac_start,omitempty"`
	LACEnd    uint16 `json:"lac_end,omitempty"`
	PNNRecord int    `json:"pnn_record,omitempty"`
	RawHex    string `json:"raw_hex,omitempty"`
}

type SIMServiceTable struct {
	Kind            string `json:"kind,omitempty"`
	RawHex          string `json:"raw_hex,omitempty"`
	EnabledServices []int  `json:"enabled_services,omitempty"`
}

// SMS 短信消息（统一数据结构）
type SMS struct {
	Index     int
	Sender    string
	Content   string
	Timestamp time.Time
}

// SMSSummary 短信列表概要
type SMSSummary struct {
	Index int
	Tag   int // 0=已读, 1=未读, 2=已发送, 3=未发送
}

// OperatingMode 操作模式（映射 AT+CFUN 值和 QMI DMS OperatingMode）
type OperatingMode int

const (
	ModeOnline   OperatingMode = 1 // AT+CFUN=1 / DMS ModeOnline
	ModeLowPower OperatingMode = 0 // AT+CFUN=0 / DMS ModeLowPower
	ModeRFOff    OperatingMode = 4 // AT+CFUN=4 / DMS ModePersistLow (飞行模式)
)
