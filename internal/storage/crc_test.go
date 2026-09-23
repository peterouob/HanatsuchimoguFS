package storage

import (
	"testing"
)

func TestCRC32(t *testing.T) {
	header := make([]byte, NeedleHeaderSize)
	payload := []byte("thisistestcrc32")
	crc := NewCRC(header, payload).Value()

	tampered := append([]byte(nil), payload...)
	tampered = append(tampered, "wrong"...)

	if crc2 := NewCRC(header, tampered).Value(); crc == crc2 {
		t.Errorf("crc did not change after tampering: both = %d", crc)
	}

	if crc3 := NewCRC(header, []byte("thisistestcrc32")).Value(); crc != crc3 {
		t.Errorf("crc not deterministic: want = %d, got = %d", crc, crc3)
	}
}

func TestCRC32_Incremental(t *testing.T) {
	whole := CRC(0).Update([]byte("abcd")).Value()
	split := CRC(0).Update([]byte("ab")).Update([]byte("cd")).Value()

	if whole != split {
		t.Errorf("incremental crc mismatch: whole = %d, split = %d", whole, split)
	}
}
