package mbim

import (
	"context"
	"fmt"
)

type DeviceServiceElement struct {
	Service UUID
	CIDs    []uint32
}

type DeviceServices struct {
	MaxDssSessions uint32
	Elements       []DeviceServiceElement
}

func (ds DeviceServices) HasService(service UUID) bool {
	for _, e := range ds.Elements {
		if e.Service.Equal(service) {
			return true
		}
	}
	return false
}

func (ds DeviceServices) Supports(service UUID, cid uint32) bool {
	for _, e := range ds.Elements {
		if !e.Service.Equal(service) {
			continue
		}
		for _, c := range e.CIDs {
			if c == cid {
				return true
			}
		}
		return false
	}
	return false
}

func parseDeviceServices(info []byte) (DeviceServices, error) {
	if len(info) < 8 {
		return DeviceServices{}, fmt.Errorf("mbim: DEVICE_SERVICES info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	count, _ := r.u32At(0)
	maxDss, _ := r.u32At(4)
	ds := DeviceServices{MaxDssSessions: maxDss}
	for i := uint32(0); i < count; i++ {
		elem, err := r.byteArrayAt(8 + int(i)*8)
		if err != nil || len(elem) < 28 {
			continue
		}
		er := newInfoReader(elem)
		var svc UUID
		copy(svc[:], elem[0:16])
		cidsCount, _ := er.u32At(24)
		if 28+int(cidsCount)*4 > len(elem) {
			continue
		}
		cids := make([]uint32, 0, cidsCount)
		for j := uint32(0); j < cidsCount; j++ {
			c, _ := er.u32At(28 + int(j)*4)
			cids = append(cids, c)
		}
		ds.Elements = append(ds.Elements, DeviceServiceElement{Service: svc, CIDs: cids})
	}
	return ds, nil
}

func QueryDeviceServices(ctx context.Context, d *Device) (DeviceServices, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectDeviceServices, CommandTypeQuery, nil)
	if err != nil {
		return DeviceServices{}, err
	}
	if resp.Status != 0 {
		return DeviceServices{}, fmt.Errorf("mbim: DEVICE_SERVICES status=%d", resp.Status)
	}
	return parseDeviceServices(resp.InfoBuffer)
}
