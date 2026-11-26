package main

import "errors"

// snappyDecode: giải nén block Snappy (định dạng thô dùng trong LevelDB,
// không phải framing của snappy stream).
func snappyDecode(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, errors.New("snappy: rỗng")
	}
	dLen, n := uvarint(src)
	if n <= 0 {
		return nil, errors.New("snappy: thiếu header")
	}
	i := n
	dst := make([]byte, 0, dLen)
	for i < len(src) {
		tag := src[i]
		i++
		switch tag & 0x03 {
		case 0: // literal
			ln := int(tag >> 2)
			if ln < 60 {
				ln++
			} else {
				extra := ln - 59
				if i+extra > len(src) {
					return nil, errors.New("snappy: literal dài quá")
				}
				v := 0
				for k := 0; k < extra; k++ {
					v |= int(src[i+k]) << (8 * k)
				}
				i += extra
				ln = v + 1
			}
			if i+ln > len(src) {
				return nil, errors.New("snappy: literal vượt biên")
			}
			dst = append(dst, src[i:i+ln]...)
			i += ln
		default: // copy
			var ln, off int
			switch tag & 0x03 {
			case 1:
				if i >= len(src) {
					return nil, errors.New("snappy: copy1 thiếu byte")
				}
				ln = 4 + int((tag>>2)&0x07)
				off = int(tag>>5)<<8 | int(src[i])
				i++
			case 2:
				if i+1 >= len(src) {
					return nil, errors.New("snappy: copy2 thiếu byte")
				}
				ln = 1 + int(tag>>2)
				off = int(src[i]) | int(src[i+1])<<8
				i += 2
			case 3:
				if i+3 >= len(src) {
					return nil, errors.New("snappy: copy4 thiếu byte")
				}
				ln = 1 + int(tag>>2)
				off = int(src[i]) | int(src[i+1])<<8 | int(src[i+2])<<16 | int(src[i+3])<<24
				i += 4
			}
			if off <= 0 || off > len(dst) {
				return nil, errors.New("snappy: offset sai")
			}
			start := len(dst) - off
			for k := 0; k < ln; k++ { // copy từng byte để xử lý đoạn chồng lấn
				dst = append(dst, dst[start+k])
			}
		}
	}
	if uint64(len(dst)) != dLen {
		return nil, errors.New("snappy: độ dài không khớp")
	}
	return dst, nil
}

// uvarint: varint không dấu kiểu protobuf, trả về (giá trị, số byte).
func uvarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; i < len(b); i++ {
		if b[i] < 0x80 {
			if i > 9 || (i == 9 && b[i] > 1) {
				return 0, -1
			}
			return x | uint64(b[i])<<s, i + 1
		}
		x |= uint64(b[i]&0x7f) << s
		s += 7
	}
	return 0, -1
}
