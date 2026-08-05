package mbim

import "testing"

func buildDeviceServicesInfo(elems []struct {
	svc  UUID
	cids []uint32
}) []byte {
	const headFixed = 8
	refList := make([]byte, len(elems)*8)
	var data []byte
	dataStart := headFixed + len(refList)
	for i, e := range elems {
		elem := make([]byte, 28+len(e.cids)*4)
		copy(elem[0:16], e.svc[:])
		le.PutUint32(elem[24:], uint32(len(e.cids)))
		for j, c := range e.cids {
			le.PutUint32(elem[28+j*4:], c)
		}
		off := uint32(dataStart + len(data))
		le.PutUint32(refList[i*8:], off)
		le.PutUint32(refList[i*8+4:], uint32(len(elem)))
		data = append(data, elem...)
	}
	info := make([]byte, dataStart+len(data))
	le.PutUint32(info[0:], uint32(len(elems)))
	le.PutUint32(info[4:], 0)
	copy(info[headFixed:], refList)
	copy(info[dataStart:], data)
	return info
}

func TestParseDeviceServices(t *testing.T) {
	info := buildDeviceServicesInfo([]struct {
		svc  UUID
		cids []uint32
	}{
		{UUIDBasicConnect, []uint32{1, 9, 11, 16}},
		{UUIDSMS, []uint32{1, 2}},
	})
	ds, err := parseDeviceServices(info)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ds.Elements) != 2 {
		t.Fatalf("elements=%d want 2", len(ds.Elements))
	}
	if !ds.Supports(UUIDBasicConnect, 16) {
		t.Fatal("应支持 BasicConnect/16")
	}
	if ds.Supports(UUIDBasicConnect, 99) {
		t.Fatal("不应支持 BasicConnect/99")
	}
	if !ds.HasService(UUIDSMS) || ds.HasService(UUIDAuth) {
		t.Fatal("HasService 判定错误")
	}
}

func TestParseDeviceServicesMalformedElementSkipped(t *testing.T) {
	info := buildDeviceServicesInfo([]struct {
		svc  UUID
		cids []uint32
	}{{UUIDBasicConnect, []uint32{1}}})
	dataStart := 8 + 1*8
	le.PutUint32(info[dataStart+24:], 99)
	ds, err := parseDeviceServices(info)
	if err != nil {
		t.Fatalf("parse 不应整体失败: %v", err)
	}
	if ds.Supports(UUIDBasicConnect, 1) {
		t.Fatal("畸形元素应被跳过")
	}
}
