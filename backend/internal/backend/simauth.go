package backend

import "context"

// SIMAuthProvider SIM 卡鉴权 / APDU 通道接口
type SIMAuthProvider interface {
	// OpenLogicalChannel 打开逻辑通道
	// AT 实现：AT+CCHO
	// QMI 实现：UIM.OpenLogicalChannel
	OpenLogicalChannel(ctx context.Context, aid string) (channelID int, err error)

	// CloseLogicalChannel 关闭逻辑通道
	// AT 实现：AT+CCHC
	// QMI 实现：UIM.CloseLogicalChannel
	CloseLogicalChannel(ctx context.Context, channelID int) error

	// TransmitAPDU 在逻辑通道上传输 APDU
	// AT 实现：AT+CGLA
	// QMI 实现：UIM.SendAPDU
	TransmitAPDU(ctx context.Context, channelID int, command string) (response string, err error)
}

type SIMAuthAIDResolver interface {
	ResolveSIMAuthAID(ctx context.Context, app string, fallbackAID string) (resolvedAID string, source string, err error)
}
