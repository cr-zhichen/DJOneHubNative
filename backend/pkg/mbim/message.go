package mbim

import "fmt"

const (
	headerLen   = 12
	fragHdrLen  = 8
	uuidLen     = 16
	openBodyLen = 4
)

func putHeader(b []byte, mt MessageType, length, txID uint32) {
	le.PutUint32(b[0:], uint32(mt))
	le.PutUint32(b[4:], length)
	le.PutUint32(b[8:], txID)
}

func encodeOpen(txID, maxControlTransfer uint32) []byte {
	b := make([]byte, headerLen+openBodyLen)
	putHeader(b, MessageTypeOpen, uint32(len(b)), txID)
	le.PutUint32(b[headerLen:], maxControlTransfer)
	return b
}

func encodeClose(txID uint32) []byte {
	b := make([]byte, headerLen)
	putHeader(b, MessageTypeClose, uint32(len(b)), txID)
	return b
}

func encodeCommand(txID uint32, service UUID, cid uint32, ct CommandType, info []byte) []byte {
	bodyLen := fragHdrLen + uuidLen + 4 + 4 + 4 + len(info)
	b := make([]byte, headerLen+bodyLen)
	putHeader(b, MessageTypeCommand, uint32(len(b)), txID)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[16:], 0)
	copy(b[20:36], service[:])
	le.PutUint32(b[36:], cid)
	le.PutUint32(b[40:], uint32(ct))
	le.PutUint32(b[44:], uint32(len(info)))
	copy(b[48:], info)
	return b
}

// Header is a decoded MBIM message header.
type Header struct {
	Type          MessageType
	Length        uint32
	TransactionID uint32
}

func decodeHeader(b []byte) (Header, error) {
	if len(b) < headerLen {
		return Header{}, fmt.Errorf("mbim: message shorter than header len=%d", len(b))
	}
	return Header{
		Type:          MessageType(le.Uint32(b[0:])),
		Length:        le.Uint32(b[4:]),
		TransactionID: le.Uint32(b[8:]),
	}, nil
}

// CommandDone is a fully reassembled COMMAND_DONE message.
type CommandDone struct {
	Service    UUID
	CID        uint32
	Status     uint32
	InfoBuffer []byte
}
