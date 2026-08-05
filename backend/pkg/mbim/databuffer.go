package mbim

import (
	"fmt"
	"unicode/utf16"
)

type infoReader struct {
	b []byte
}

func newInfoReader(b []byte) *infoReader {
	return &infoReader{b: b}
}

func (r *infoReader) u32At(pos int) (uint32, error) {
	if pos < 0 || pos > len(r.b)-4 {
		return 0, fmt.Errorf("mbim info buffer u32 at %d out of range", pos)
	}
	return le.Uint32(r.b[pos : pos+4]), nil
}

func (r *infoReader) u64At(pos int) (uint64, error) {
	if pos < 0 || pos > len(r.b)-8 {
		return 0, fmt.Errorf("mbim info buffer u64 at %d out of range", pos)
	}
	return le.Uint64(r.b[pos : pos+8]), nil
}

func (r *infoReader) stringArrayCountAt(countPos int) ([]string, error) {
	count, err := r.u32At(countPos)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		s, err := r.stringAt(countPos + 4 + int(i)*8)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *infoReader) uiccByteArrayAt(pairPos int) ([]byte, error) {
	size, err := r.u32At(pairPos)
	if err != nil {
		return nil, err
	}
	offset, err := r.u32At(pairPos + 4)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	end := uint64(offset) + uint64(size)
	if end > uint64(len(r.b)) {
		return nil, fmt.Errorf("mbim: uicc byte-array offset %d size %d out of range (buf %d)", offset, size, len(r.b))
	}
	return append([]byte(nil), r.b[offset:end]...), nil
}

func (r *infoReader) byteArrayAt(pairPos int) ([]byte, error) {
	offset, err := r.u32At(pairPos)
	if err != nil {
		return nil, err
	}
	size, err := r.u32At(pairPos + 4)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	end := uint64(offset) + uint64(size)
	if end > uint64(len(r.b)) {
		return nil, fmt.Errorf("mbim: byte-array offset %d size %d out of range (buf %d)", offset, size, len(r.b))
	}
	return append([]byte(nil), r.b[offset:end]...), nil
}

func (r *infoReader) stringAt(pairPos int) (string, error) {
	offset, err := r.u32At(pairPos)
	if err != nil {
		return "", err
	}
	length, err := r.u32At(pairPos + 4)
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	return decodeUTF16Range(r.b, offset, length)
}

func decodeUTF16Range(b []byte, offset, length uint32) (string, error) {
	if length == 0 {
		return "", nil
	}
	if length%2 != 0 {
		return "", fmt.Errorf("mbim info buffer string has odd UTF-16LE length %d", length)
	}
	end := uint64(offset) + uint64(length)
	if end > uint64(len(b)) {
		return "", fmt.Errorf("mbim info buffer string offset %d length %d out of range", offset, length)
	}

	start := int(offset)
	limit := int(end)
	units := make([]uint16, 0, int(length/2))
	for pos := start; pos < limit; pos += 2 {
		units = append(units, le.Uint16(b[pos:pos+2]))
	}
	return string(utf16.Decode(units)), nil
}
