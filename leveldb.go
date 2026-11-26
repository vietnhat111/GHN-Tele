package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Đọc LevelDB của Chrome (thư mục Local Storage/leveldb) chỉ để lấy giá trị
// localStorage của một origin. Không cần quyền ghi, không sửa dữ liệu.

const sstMagic = 0xdb4775248b80fb57

type ledEntry struct {
	seq uint64
	val []byte
	del bool // bản ghi xoá (tombstone)
}

// mergeEntry: giữ bản có số thứ tự (seq) lớn nhất — đó là bản mới nhất,
// bất kể nằm ở .log hay .ldb nào.
func mergeEntry(out map[string]ledEntry, key string, e ledEntry) {
	if cur, ok := out[key]; ok && cur.seq > e.seq {
		return
	}
	out[key] = e
}

// parseInternalKey: khoá trong .ldb = khoá thật + 8 byte (seq<<8 | loại).
// Loại 1 = giá trị, 0 = xoá. Không đúng dạng thì coi như khoá thật, seq 0.
func parseInternalKey(k []byte) (user []byte, seq uint64, del bool) {
	if len(k) < 8 {
		return k, 0, false
	}
	tag := binary.LittleEndian.Uint64(k[len(k)-8:])
	switch tag & 0xff {
	case 0:
		return k[:len(k)-8], tag >> 8, true
	case 1:
		return k[:len(k)-8], tag >> 8, false
	}
	return k, 0, false
}

// readLevelDB trả về map khoá → giá trị (bản mới nhất thắng).
func readLevelDB(dir string) (map[string][]byte, []string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var logs, tables []string
	var warns []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".log"):
			logs = append(logs, name)
		case strings.HasSuffix(name, ".ldb"), strings.HasSuffix(name, ".sst"):
			tables = append(tables, name)
		}
	}
	// .ldb tăng dần theo số file: file số lớn là bản mới hơn
	sort.Slice(tables, func(i, j int) bool { return fileNum(tables[i]) < fileNum(tables[j]) })

	merged := map[string]ledEntry{}
	for _, name := range tables {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for _, kv := range readSSTable(b) {
			uk, seq, del := parseInternalKey(kv.Key)
			mergeEntry(merged, string(uk), ledEntry{seq: seq, val: kv.Value, del: del})
		}
	}
	for _, name := range logs {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for k, e := range readLogFile(b) {
			mergeEntry(merged, k, e)
		}
	}
	out := map[string][]byte{}
	for k, e := range merged {
		if !e.del {
			out[k] = e.val
		}
	}
	return out, warns, nil
}

func fileNum(name string) int {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".log"), ".ldb")
	base = strings.TrimSuffix(base, ".sst")
	n, _ := strconv.Atoi(base)
	return n
}

// ---------------------------------------------------------------- .log

const logBlockSize = 32768

// readLogFile: đọc log record (32KB/block, header 7 byte) và giải WriteBatch.
func readLogFile(b []byte) map[string]ledEntry {
	out := map[string]ledEntry{}
	var pending []byte
	for off := 0; off+7 <= len(b); {
		blockEnd := off + logBlockSize
		if blockEnd > len(b) {
			blockEnd = len(b)
		}
		handled := false
		for off+7 <= blockEnd {
			length := int(binary.LittleEndian.Uint16(b[off+4 : off+6]))
			typ := b[off+6]
			if length == 0 && typ == 0 { // phần đệm cuối block
				off = blockEnd
				handled = true
				break
			}
			if off+7+length > blockEnd {
				off = blockEnd
				handled = true
				break
			}
			frag := b[off+7 : off+7+length]
			off += 7 + length
			switch typ {
			case 1: // FULL
				applyWriteBatch(frag, out)
				pending = pending[:0]
			case 2: // FIRST
				pending = append(pending[:0], frag...)
			case 3: // MIDDLE
				pending = append(pending, frag...)
			case 4: // LAST
				pending = append(pending, frag...)
				applyWriteBatch(pending, out)
				pending = pending[:0]
			}
		}
		if !handled && off >= len(b) {
			break
		}
	}
	return out
}

func applyWriteBatch(rec []byte, out map[string]ledEntry) {
	if len(rec) < 12 {
		return
	}
	seq := binary.LittleEndian.Uint64(rec[0:8])
	count := binary.LittleEndian.Uint32(rec[8:12])
	p := 12
	for i := uint32(0); i < count && p < len(rec); i++ {
		tag := rec[p]
		p++
		klen, n := uvarint(rec[p:])
		if n <= 0 || p+n+int(klen) > len(rec) {
			return
		}
		p += n
		key := rec[p : p+int(klen)]
		p += int(klen)
		switch tag {
		case 0: // xoá
			mergeEntry(out, string(key), ledEntry{seq: seq + uint64(i), del: true})
		case 1: // đặt giá trị
			vlen, n := uvarint(rec[p:])
			if n <= 0 || p+n+int(vlen) > len(rec) {
				return
			}
			p += n
			val := append([]byte(nil), rec[p:p+int(vlen)]...)
			p += int(vlen)
			mergeEntry(out, string(key), ledEntry{seq: seq + uint64(i), val: val})
		default:
			return
		}
	}
}

// ---------------------------------------------------------------- .ldb

type sstKV struct {
	Key   []byte
	Value []byte
}

func readSSTable(b []byte) []sstKV {
	if len(b) < 48 {
		return nil
	}
	footer := b[len(b)-48:]
	p := 0
	if _, n := uvarint(footer[p:]); n > 0 {
		p += n
	} else {
		return nil
	}
	if _, n := uvarint(footer[p:]); n > 0 {
		p += n
	} else {
		return nil
	}
	iOff, n1 := uvarint(footer[p:])
	p += n1
	iSize, n2 := uvarint(footer[p:])
	if n1 <= 0 || n2 <= 0 {
		return nil
	}
	idx, err := readBlock(b, iOff, iSize)
	if err != nil {
		return nil
	}
	var out []sstKV
	_ = iterBlock(idx, func(_, v []byte) error {
		off, n1 := uvarint(v)
		if n1 <= 0 {
			return nil
		}
		size, n2 := uvarint(v[n1:])
		if n2 <= 0 {
			return nil
		}
		blk, err := readBlock(b, off, size)
		if err != nil {
			return nil
		}
		return iterBlock(blk, func(k, val []byte) error {
			out = append(out, sstKV{
				Key:   append([]byte(nil), k...),
				Value: append([]byte(nil), val...),
			})
			return nil
		})
	})
	return out
}

// readBlock: lấy nội dung block tại (off,size), giải nén nếu cần.
func readBlock(b []byte, off, size uint64) ([]byte, error) {
	end := off + size
	if end+5 > uint64(len(b)) {
		return nil, fmt.Errorf("block vượt biên")
	}
	body := b[off:end]
	switch b[end] {
	case 0: // không nén
		return body, nil
	case 1: // snappy
		return snappyDecode(body)
	default:
		return nil, fmt.Errorf("kiểu nén %d chưa hỗ trợ", b[end])
	}
}

// iterBlock: duyệt entry trong block (prefix-compressed + restart array).
func iterBlock(blk []byte, emit func(k, v []byte) error) error {
	if len(blk) < 4 {
		return fmt.Errorf("block rỗng")
	}
	nRestart := int(binary.LittleEndian.Uint32(blk[len(blk)-4:]))
	limit := len(blk) - 4 - 4*nRestart
	if nRestart < 0 || limit < 0 || limit > len(blk) {
		return fmt.Errorf("restart array sai")
	}
	p := 0
	key := []byte{}
	for p < limit {
		shared, n1 := uvarint(blk[p:])
		if n1 <= 0 {
			return fmt.Errorf("shared sai")
		}
		p += n1
		notShared, n2 := uvarint(blk[p:])
		if n2 <= 0 {
			return fmt.Errorf("non_shared sai")
		}
		p += n2
		vlen, n3 := uvarint(blk[p:])
		if n3 <= 0 {
			return fmt.Errorf("vlen sai")
		}
		p += n3
		if shared > uint64(len(key)) || p+int(notShared)+int(vlen) > len(blk) {
			return fmt.Errorf("entry vượt biên")
		}
		key = append(key[:shared], blk[p:p+int(notShared)]...)
		p += int(notShared)
		val := blk[p : p+int(vlen)]
		p += int(vlen)
		if err := emit(key, val); err != nil {
			return err
		}
	}
	return nil
}
