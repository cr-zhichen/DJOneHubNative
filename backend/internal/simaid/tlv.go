package simaid

func CollectTLVValues(data []byte, want byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(data); {
		for i < len(data) && (data[i] == 0x00 || data[i] == 0xFF) {
			i++
		}
		if i >= len(data) {
			break
		}
		tagStart := i
		i++
		if data[tagStart]&0x1F == 0x1F {
			for i < len(data) {
				b := data[i]
				i++
				if b&0x80 == 0 {
					break
				}
			}
		}
		if i >= len(data) {
			break
		}
		tag := data[tagStart:i]

		lengthByte := data[i]
		i++
		length := int(lengthByte)
		if lengthByte&0x80 != 0 {
			n := int(lengthByte & 0x7F)
			if n <= 0 || n > 3 || i+n > len(data) {
				break
			}
			length = 0
			for j := 0; j < n; j++ {
				length = (length << 8) | int(data[i+j])
			}
			i += n
		}
		if length < 0 || i+length > len(data) {
			break
		}
		value := data[i : i+length]
		i += length

		if len(tag) == 1 && tag[0] == want {
			out = append(out, append([]byte(nil), value...))
		}
		if len(tag) > 0 && tag[0]&0x20 != 0 {
			out = append(out, CollectTLVValues(value, want)...)
		}
	}
	return out
}
