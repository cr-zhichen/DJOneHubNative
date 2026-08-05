package mbim

import (
	"context"
	"fmt"
)

// UICCApplication 是 MS UICC Application List 里的一个应用项。
type UICCApplication struct {
	Type uint32 // MbimUiccApplicationType(2=USIM,3=ISIM,...)
	AID  []byte // 完整应用标识(ApplicationId)
}

// QueryUICCApplicationList 通过 MS UICC Low Level Access 的 APPLICATION_LIST(CID 7)直读
// 卡上应用列表(含完整 AID),无需手动开逻辑通道/选 EF_DIR。
func QueryUICCApplicationList(ctx context.Context, d *Device) ([]UICCApplication, error) {
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCApplicationList, CommandTypeQuery, nil)
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, &StatusError{Op: "UICC_APPLICATION_LIST", Status: resp.Status}
	}
	return parseUICCApplicationList(resp.InfoBuffer)
}

// UICCFileResult 是直读文件(ReadBinary/ReadRecord)的结果:卡状态字 + 数据。
type UICCFileResult struct {
	SW1  uint32
	SW2  uint32
	Data []byte
}

// encodeUICCReadBinary 按 libmbim Read Binary 的 query 布局编码(固定 44 字节 + 变长区)。
// 字段:Version, AppId(ref), FilePath(ref), ReadOffset, ReadSize, LocalPin(string,空), Data(ref,空)。
func encodeUICCReadBinary(aid, filePath []byte, readOffset, readSize uint32) []byte {
	const fixed = 44
	aidPad := pad4(len(aid))
	pathPad := pad4(len(filePath))
	info := make([]byte, fixed+aidPad+pathPad)
	le.PutUint32(info[0:], 1) // Version
	aidOff := fixed
	le.PutUint32(info[4:], uint32(aidOff))
	le.PutUint32(info[8:], uint32(len(aid)))
	copy(info[aidOff:], aid)
	pathOff := fixed + aidPad
	le.PutUint32(info[12:], uint32(pathOff))
	le.PutUint32(info[16:], uint32(len(filePath)))
	copy(info[pathOff:], filePath)
	le.PutUint32(info[20:], readOffset)
	le.PutUint32(info[24:], readSize)
	// LocalPin(28/32)与 Data(36/40)留空:offset=0,size=0。
	return info
}

// UICCReadBinary 通过 READ_BINARY(CID 9)直读透明 EF:给出完整 AID 与文件路径,
// 模组内部完成选应用/选文件,无需手动开逻辑通道。
func UICCReadBinary(ctx context.Context, d *Device, aid, filePath []byte, readOffset, readSize uint32) (UICCFileResult, error) {
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCReadBinary, CommandTypeQuery, encodeUICCReadBinary(aid, filePath, readOffset, readSize))
	if err != nil {
		return UICCFileResult{}, err
	}
	if resp.Status != 0 {
		return UICCFileResult{}, &StatusError{Op: "UICC_READ_BINARY", Status: resp.Status}
	}
	return parseUICCFileResponse(resp.InfoBuffer)
}

// encodeUICCReadRecord 按 libmbim Read Record 的 query 布局编码(固定 40 字节 + 变长区)。
// 字段:Version, AppId(ref), FilePath(ref), RecordNumber, LocalPin(string,空), Data(ref,空)。
func encodeUICCReadRecord(aid, filePath []byte, recordNumber uint32) []byte {
	const fixed = 40
	aidPad := pad4(len(aid))
	pathPad := pad4(len(filePath))
	info := make([]byte, fixed+aidPad+pathPad)
	le.PutUint32(info[0:], 1) // Version
	aidOff := fixed
	le.PutUint32(info[4:], uint32(aidOff))
	le.PutUint32(info[8:], uint32(len(aid)))
	copy(info[aidOff:], aid)
	pathOff := fixed + aidPad
	le.PutUint32(info[12:], uint32(pathOff))
	le.PutUint32(info[16:], uint32(len(filePath)))
	copy(info[pathOff:], filePath)
	le.PutUint32(info[20:], recordNumber)
	// LocalPin(24/28)与 Data(32/36)留空。
	return info
}

// UICCReadRecord 通过 READ_RECORD(CID 10)直读线性记录 EF(如 EF_MSISDN)。
func UICCReadRecord(ctx context.Context, d *Device, aid, filePath []byte, recordNumber uint32) (UICCFileResult, error) {
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCReadRecord, CommandTypeQuery, encodeUICCReadRecord(aid, filePath, recordNumber))
	if err != nil {
		return UICCFileResult{}, err
	}
	if resp.Status != 0 {
		return UICCFileResult{}, &StatusError{Op: "UICC_READ_RECORD", Status: resp.Status}
	}
	return parseUICCFileResponse(resp.InfoBuffer)
}

// parseUICCFileResponse 解析 ReadBinary/ReadRecord 应答:Version, SW1, SW2, Data(ref)。
func parseUICCFileResponse(info []byte) (UICCFileResult, error) {
	r := newInfoReader(info)
	sw1, err := r.u32At(4)
	if err != nil {
		return UICCFileResult{}, err
	}
	sw2, err := r.u32At(8)
	if err != nil {
		return UICCFileResult{}, err
	}
	data, err := r.byteArrayAt(12)
	if err != nil {
		return UICCFileResult{}, err
	}
	return UICCFileResult{SW1: sw1, SW2: sw2, Data: data}, nil
}

func parseUICCApplicationList(info []byte) ([]UICCApplication, error) {
	r := newInfoReader(info)
	count, err := r.u32At(4) // ApplicationCount
	if err != nil {
		return nil, err
	}
	const ptrStart = 16 // Version+Count+ActiveIndex+ListSizeBytes
	apps := make([]UICCApplication, 0, count)
	for i := uint32(0); i < count; i++ {
		off, err := r.u32At(ptrStart + int(i)*8)
		if err != nil {
			return nil, err
		}
		size, err := r.u32At(ptrStart + int(i)*8 + 4)
		if err != nil {
			return nil, err
		}
		if uint64(off)+uint64(size) > uint64(len(info)) {
			return nil, fmt.Errorf("mbim: UICC application %d struct out of range off=%d size=%d", i, off, size)
		}
		// 结构内字段的 offset 相对结构起始,故对结构 blob 单独建 reader。
		sr := newInfoReader(info[off : uint64(off)+uint64(size)])
		appType, err := sr.u32At(0)
		if err != nil {
			return nil, err
		}
		aid, err := sr.byteArrayAt(4) // ApplicationId: ref-byte-array(offset,size)
		if err != nil {
			return nil, err
		}
		apps = append(apps, UICCApplication{Type: appType, AID: aid})
	}
	return apps, nil
}
