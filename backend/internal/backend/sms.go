package backend

import "context"

// SMSProvider 短信收发接口
type SMSProvider interface {
	// SendSMS 发送短信
	// AT 实现：AT+CMGS (PDU 模式)
	// QMI 实现：WMS.SendRawMessage
	SendSMS(ctx context.Context, to, body string) error

	// ReadSMS 读取指定索引的短信
	// AT 实现：AT+CMGR
	// QMI 实现：WMS.RawReadMessage
	ReadSMS(ctx context.Context, index int) (*SMS, error)

	// DeleteSMS 删除指定索引的短信
	// AT 实现：AT+CMGD
	// QMI 实现：WMS.DeleteMessage
	DeleteSMS(ctx context.Context, index int) error

	// ListSMS 列出所有短信概要
	// AT 实现：AT+CMGL=4
	// QMI 实现：WMS.ListMessages
	ListSMS(ctx context.Context) ([]SMSSummary, error)

	// DeleteAllSMS 删除所有短信
	// AT 实现：AT+CMGD=1,4
	// QMI 实现：WMS.DeleteMessagesByTag（遍历所有 tag）
	DeleteAllSMS(ctx context.Context) error
}
