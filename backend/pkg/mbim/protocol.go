// Package mbim implements MBIM control-plane protocol helpers.
package mbim

import (
	"encoding/binary"
	"fmt"
)

// MessageType is the MBIM control message type.
type MessageType uint32

const (
	MessageTypeOpen           MessageType = 0x00000001
	MessageTypeClose          MessageType = 0x00000002
	MessageTypeCommand        MessageType = 0x00000003
	MessageTypeHostError      MessageType = 0x00000004
	MessageTypeOpenDone       MessageType = 0x80000001
	MessageTypeCloseDone      MessageType = 0x80000002
	MessageTypeCommandDone    MessageType = 0x80000003
	MessageTypeFunctionError  MessageType = 0x80000004
	MessageTypeIndicateStatus MessageType = 0x80000007
)

// CommandType is the MBIM COMMAND command_type field.
type CommandType uint32

const (
	CommandTypeQuery CommandType = 0
	CommandTypeSet   CommandType = 1
)

// Service is an MBIM service identifier.
type Service uint32

const (
	ServiceBasicConnect Service = 1
	ServiceSMS          Service = 2
	ServiceUSSD         Service = 3
	ServiceProxyControl Service = 10
)

// Basic Connect CIDs.
const (
	CIDBasicConnectDeviceCaps                 uint32 = 1
	CIDBasicConnectSubscriberReadyStatus      uint32 = 2
	CIDBasicConnectRadioState                 uint32 = 3
	CIDBasicConnectHomeProvider               uint32 = 7
	CIDBasicConnectVisibleProviders           uint32 = 8
	CIDBasicConnectRegisterState              uint32 = 9
	CIDBasicConnectPacketService              uint32 = 10
	CIDBasicConnectSignalState                uint32 = 11
	CIDBasicConnectConnect                    uint32 = 12
	CIDBasicConnectIPConfiguration            uint32 = 15
	CIDBasicConnectDeviceServices             uint32 = 16
	CIDBasicConnectDeviceServiceSubscribeList uint32 = 19
)

// REGISTER_STATE actions.
const (
	RegisterActionAutomatic uint32 = 0
	RegisterActionManual    uint32 = 1
)

// REGISTER_STATE response register modes.
const (
	RegisterModeUnknown   uint32 = 0
	RegisterModeAutomatic uint32 = 1
	RegisterModeManual    uint32 = 2
)

// MBIM cellular classes.
const (
	CellularClassGSM  uint32 = 1
	CellularClassCDMA uint32 = 2
)

// Proxy Control CIDs.
const (
	CIDProxyControlConfiguration uint32 = 1
)

// SMS CIDs.
const (
	CIDSMSConfiguration uint32 = 1
	CIDSMSRead          uint32 = 2
	CIDSMSSend          uint32 = 3
	CIDSMSDelete        uint32 = 4
)

// USSD CIDs and actions.
const (
	CIDUSSD            uint32 = 1
	USSDActionInitiate uint32 = 0
	USSDActionContinue uint32 = 1
	USSDActionCancel   uint32 = 2
)

// USSD response codes.
const (
	USSDRespNoActionRequired      uint32 = 0
	USSDRespActionRequired        uint32 = 1
	USSDRespTerminated            uint32 = 2
	USSDRespOtherLocalClient      uint32 = 3
	USSDRespOperationNotSupported uint32 = 4
	USSDRespNetworkTimeout        uint32 = 5
)

// SMS read/delete flags.
const (
	SMSFlagAll   uint32 = 0
	SMSFlagIndex uint32 = 1
	SMSFlagNew   uint32 = 2
)

// SMS formats.
const (
	SMSFormatPDU  uint32 = 0
	SMSFormatCDMA uint32 = 1
)

// MS UICC Low Level Access CIDs.
const (
	CIDUICCOpenChannel     uint32 = 2
	CIDUICCCloseChannel    uint32 = 3
	CIDUICCAPDU            uint32 = 4
	CIDUICCApplicationList uint32 = 7
	CIDUICCFileStatus      uint32 = 8
	CIDUICCReadBinary      uint32 = 9
	CIDUICCReadRecord      uint32 = 10
)

// Auth service CIDs.
const (
	CIDAuthAKA  uint32 = 1
	CIDAuthAKAP uint32 = 2
	CIDAuthSIM  uint32 = 3
)

// MS Basic Connect Extensions CIDs.
const (
	CIDMSBasicConnectExtDeviceReset    uint32 = 10
	CIDMSBasicConnectExtSlotInfoStatus uint32 = 8
	CIDMSBasicConnectExtVersion        uint32 = 15
)

// UUID is a 16-byte MBIM service UUID stored in wire byte order.
type UUID [16]byte

// String renders the UUID in canonical 8-4-4-4-12 hexadecimal form.
func (u UUID) String() string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// Equal reports whether two UUIDs contain identical wire bytes.
func (u UUID) Equal(other UUID) bool {
	return u == other
}

var (
	UUIDBasicConnect         = UUID{0xa2, 0x89, 0xcc, 0x33, 0xbc, 0xbb, 0x8b, 0x4f, 0xb6, 0xb0, 0x13, 0x3e, 0xc2, 0xaa, 0xe6, 0xdf}
	UUIDSMS                  = UUID{0x53, 0x3f, 0xbe, 0xeb, 0x14, 0xfe, 0x44, 0x67, 0x9f, 0x90, 0x33, 0xa2, 0x23, 0xe5, 0x6c, 0x3f}
	UUIDUSSD                 = UUID{0xe5, 0x50, 0xa0, 0xc8, 0x5e, 0x82, 0x47, 0x9e, 0x82, 0xf7, 0x10, 0xab, 0xf4, 0xc3, 0x35, 0x1f}
	UUIDProxyControl         = UUID{0x83, 0x8c, 0xf7, 0xfb, 0x8d, 0x0d, 0x4d, 0x7f, 0x87, 0x1e, 0xd7, 0x1d, 0xbe, 0xfb, 0xb3, 0x9b}
	UUIDMSUICCLowLevelAccess = UUID{0xc2, 0xf6, 0x58, 0x8e, 0xf0, 0x37, 0x4b, 0xc9, 0x86, 0x65, 0xf4, 0xd4, 0x4b, 0xd0, 0x93, 0x67}

	UUIDMSBasicConnectExtensions = UUID{0x3d, 0x01, 0xdc, 0xc5, 0xfe, 0xf5, 0x4d, 0x05, 0x0d, 0x3a, 0xbe, 0xf7, 0x05, 0x8e, 0x9a, 0xaf}
	UUIDAuth                     = UUID{0x1d, 0x2b, 0x5f, 0xf7, 0x0a, 0xa1, 0x48, 0xb2, 0xaa, 0x52, 0x50, 0xf1, 0x57, 0x67, 0x17, 0x4e}
	UUIDContextTypeInternet      = UUID{0x7e, 0x5e, 0x2a, 0x7e, 0x4e, 0x6f, 0x72, 0x72, 0x73, 0x6b, 0x65, 0x6e, 0x7e, 0x5e, 0x2a, 0x7e}
	UUIDQMI                      = UUID{0xd1, 0xa3, 0x0b, 0xc2, 0xf9, 0x7a, 0x6e, 0x43, 0xbf, 0x65, 0xc7, 0xe2, 0x4f, 0xb0, 0xf0, 0xd3}
)

// QMI CIDs.
const (
	CIDQMIMsg uint32 = 1
)

// le is the package-wide little-endian byte order for MBIM wire fields.
var le = binary.LittleEndian
