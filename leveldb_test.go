package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------- helpers

func putBlock(entries [][2][]byte) []byte {
	var b []byte
	for _, e := range entries {
		b = binary.AppendUvarint(b, 0) // shared = 0 (test không nén tiền tố)
		b = binary.AppendUvarint(b, uint64(len(e[0])))
		b = binary.AppendUvarint(b, uint64(len(e[1])))
		b = append(b, e[0]...)
		b = append(b, e[1]...)
	}
	b = binary.LittleEndian.AppendUint32(b, 0) // restart[0] = 0
	b = binary.LittleEndian.AppendUint32(b, 1) // 1 restart point
	return b
}

func withTrailer(block []byte, ctype byte) []byte {
	out := append([]byte(nil), block...)
	out = append(out, ctype, 0, 0, 0, 0) // crc không kiểm khi đọc
	return out
}

// writeSST: dựng file .ldb tối giản (1 data block + 1 index block, không nén).
func writeSST(kvs [][2][]byte) []byte {
	data := putBlock(kvs)
	out := withTrailer(data, 0)

	handle := binary.AppendUvarint(nil, 0)
	handle = binary.AppendUvarint(handle, uint64(len(data)))
	idx := putBlock([][2][]byte{{[]byte("z~"), handle}})
	idxOffset := len(out)
	out = append(out, withTrailer(idx, 0)...)

	f := binary.AppendUvarint(nil, 0) // metaindex offset
	f = binary.AppendUvarint(f, 0)    // metaindex size
	f = binary.AppendUvarint(f, uint64(idxOffset))
	f = binary.AppendUvarint(f, uint64(len(idx)))
	for len(f) < 40 {
		f = append(f, 0)
	}
	f = binary.LittleEndian.AppendUint64(f, sstMagic)
	return append(out, f...)
}

// writeLog: dựng file .log với 1 WriteBatch FULL record.
func writeLog(kvs [][2][]byte, seq uint64) []byte {
	rec := binary.LittleEndian.AppendUint64(nil, seq)
	rec = binary.LittleEndian.AppendUint32(rec, uint32(len(kvs)))
	for _, e := range kvs {
		rec = append(rec, 1) // kTypeValue
		rec = binary.AppendUvarint(rec, uint64(len(e[0])))
		rec = append(rec, e[0]...)
		rec = binary.AppendUvarint(rec, uint64(len(e[1])))
		rec = append(rec, e[1]...)
	}
	hdr := make([]byte, 7)
	binary.LittleEndian.PutUint16(hdr[4:], uint16(len(rec)))
	hdr[6] = 1 // FULL
	return append(hdr, rec...)
}

// ---------------------------------------------------------------- snappy

func TestSnappyLiteral(t *testing.T) {
	// header = 5, tag literal len 5 → (5-1)<<2 = 16
	src := []byte{5, 16, 'h', 'e', 'l', 'l', 'o'}
	got, err := snappyDecode(src)
	if err != nil || string(got) != "hello" {
		t.Fatalf("snappy literal: %q %v", got, err)
	}
}

func TestSnappyCopy(t *testing.T) {
	// "ab" + copy len 6 offset 2 → "abababab"
	src := []byte{8, 4, 'a', 'b', 9, 2}
	got, err := snappyDecode(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abababab" {
		t.Fatalf("snappy copy = %q", got)
	}
}

func TestSnappyBadInput(t *testing.T) {
	if _, err := snappyDecode(nil); err == nil {
		t.Error("rỗng phải lỗi")
	}
	if _, err := snappyDecode([]byte{10, 0x01, 0x7f}); err == nil {
		t.Error("offset sai phải lỗi")
	}
}

// ---------------------------------------------------------------- sstable

func TestSSTableRoundTrip(t *testing.T) {
	kvs := [][2][]byte{
		{[]byte("_https://nhanh.ghn.vn\x00\x01SESSION"), []byte("token-abc")},
		{[]byte("_https://nhanh.ghn.vn\x00\x01CURRENT_HUB_SORTING"), []byte("2212700")},
		{[]byte("key3"), []byte("v3")},
	}
	got := readSSTable(writeSST(kvs))
	if len(got) != len(kvs) {
		t.Fatalf("đọc %d/%d khoá", len(got), len(kvs))
	}
	for i, kv := range got {
		if string(kv.Key) != string(kvs[i][0]) || string(kv.Value) != string(kvs[i][1]) {
			t.Errorf("entry %d: %q=%q", i, kv.Key, kv.Value)
		}
	}
}

func TestSSTableCompressedRejected(t *testing.T) {
	// block ghi kiểu nén lạ (zstd=2) → phải bỏ qua, không panic
	data := putBlock([][2][]byte{{[]byte("a"), []byte("b")}})
	out := withTrailer(data, 2)
	_ = out
	got := readSSTable(out)
	if len(got) != 0 {
		t.Errorf("block nén lạ phải bị bỏ qua, đọc được %d", len(got))
	}
}

// ---------------------------------------------------------------- leveldb

func TestLevelDBLogAndOverride(t *testing.T) {
	dir := t.TempDir()
	old := [][2][]byte{{[]byte("A"), []byte("old")}, {[]byte("B"), []byte("b")}}
	if err := os.WriteFile(filepath.Join(dir, "000002.ldb"), writeSST(old), 0600); err != nil {
		t.Fatal(err)
	}
	newer := [][2][]byte{{[]byte("A"), []byte("new")}}
	if err := os.WriteFile(filepath.Join(dir, "000003.log"), writeLog(newer, 100), 0600); err != nil {
		t.Fatal(err)
	}
	vals, warns, err := readLevelDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("cảnh báo lạ: %v", warns)
	}
	if string(vals["A"]) != "new" {
		t.Errorf("log phải ghi đè .ldb, được %q", vals["A"])
	}
	if string(vals["B"]) != "b" {
		t.Errorf("mất khoá cũ B: %q", vals["B"])
	}
}

func TestLocalStorageKeys(t *testing.T) {
	dir := t.TempDir()
	raw := [][2][]byte{
		{[]byte("_https://nhanh.ghn.vn\x00\x01SESSION"), append([]byte{1}, []byte("eyJhbGciOi.J9.abc")...)},
		{[]byte("_https://nhanh.ghn.vn\x00\x01CURRENT_HUB_SORTING"), append([]byte{1}, []byte("2212700")...)},
		{[]byte("_https://example.com\x00\x01SESSION"), append([]byte{1}, []byte("không-lấy")...)},
	}
	if err := os.WriteFile(filepath.Join(dir, "000005.log"), writeLog(raw, 1), 0600); err != nil {
		t.Fatal(err)
	}
	vals, _, err := localStorageKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if vals["SESSION"] != "eyJhbGciOi.J9.abc" {
		t.Errorf("SESSION = %q", vals["SESSION"])
	}
	if vals["CURRENT_HUB_SORTING"] != "2212700" {
		t.Errorf("hub = %q", vals["CURRENT_HUB_SORTING"])
	}
	if len(vals) != 2 {
		t.Errorf("chỉ được lấy khoá của ghn.vn, được %d", len(vals))
	}
}

func TestDecodeLSValue(t *testing.T) {
	if got := decodeLSValue(append([]byte{1}, []byte("abc")...)); got != "abc" {
		t.Errorf("latin1: %q", got)
	}
	// "ab" UTF-16LE
	if got := decodeLSValue([]byte{0, 'a', 0, 'b', 0}); got != "ab" {
		t.Errorf("utf16: %q", got)
	}
	if got := decodeLSValue(nil); got != "" {
		t.Errorf("rỗng: %q", got)
	}
}

// ikey: khoá nội bộ đúng như Chrome ghi trong .ldb (khoá + seq<<8|loại).
func ikey(k string, seq uint64, typ byte) []byte {
	return binary.LittleEndian.AppendUint64([]byte(k), seq<<8|uint64(typ))
}

func TestLevelDBRealLdbKeys(t *testing.T) {
	dir := t.TempDir()
	sess := "_https://nhanh.ghn.vn\x00\x01SESSION"
	hub := "_https://nhanh.ghn.vn\x00\x01CURRENT_HUB_SORTING"
	// Chrome đã gom SESSION vào .ldb (không còn trong .log) — trường hợp sau khi khởi động lại
	ldb := [][2][]byte{
		{ikey(hub, 5, 1), append([]byte{1}, "2212700"...)},
		{ikey(sess, 9, 1), append([]byte{1}, "eyJ.moi.nhat"...)},
		{ikey(sess, 3, 1), append([]byte{1}, "eyJ.cu"...)}, // cùng khoá, bản cũ hơn
	}
	if err := os.WriteFile(filepath.Join(dir, "000010.ldb"), writeSST(ldb), 0600); err != nil {
		t.Fatal(err)
	}
	vals, _, err := localStorageKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if vals["SESSION"] != "eyJ.moi.nhat" {
		t.Errorf("phải đọc SESSION mới nhất trong .ldb, được %q", vals["SESSION"])
	}
	if vals["CURRENT_HUB_SORTING"] != "2212700" {
		t.Errorf("mã kho trong .ldb: %q", vals["CURRENT_HUB_SORTING"])
	}

	// file số nhỏ hơn nhưng seq lớn hơn vẫn thắng
	newer := [][2][]byte{{ikey(sess, 20, 1), append([]byte{1}, "eyJ.seq20"...)}}
	if err := os.WriteFile(filepath.Join(dir, "000004.ldb"), writeSST(newer), 0600); err != nil {
		t.Fatal(err)
	}
	vals, _, _ = localStorageKeys(dir)
	if vals["SESSION"] != "eyJ.seq20" {
		t.Errorf("seq lớn nhất phải thắng, được %q", vals["SESSION"])
	}

	// đăng xuất: .log ghi bản xoá với seq mới hơn → không được trả SESSION cũ
	del := writeLogDel(sess, 30)
	if err := os.WriteFile(filepath.Join(dir, "000011.log"), del, 0600); err != nil {
		t.Fatal(err)
	}
	vals, _, _ = localStorageKeys(dir)
	if _, ok := vals["SESSION"]; ok {
		t.Errorf("đã xoá (đăng xuất) thì không trả SESSION cũ: %q", vals["SESSION"])
	}
}

// writeLogDel: .log với 1 bản ghi xoá.
func writeLogDel(key string, seq uint64) []byte {
	rec := binary.LittleEndian.AppendUint64(nil, seq)
	rec = binary.LittleEndian.AppendUint32(rec, 1)
	rec = append(rec, 0) // loại xoá
	rec = binary.AppendUvarint(rec, uint64(len(key)))
	rec = append(rec, key...)
	hdr := make([]byte, 7)
	binary.LittleEndian.PutUint16(hdr[4:6], uint16(len(rec)))
	hdr[6] = 1 // FULL
	return append(hdr, rec...)
}
