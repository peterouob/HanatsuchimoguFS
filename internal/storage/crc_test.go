package storage

import (
	"testing"
)

func TestCRC32(t *testing.T) {
	payload := []byte("thisistestcrc32")
	crc := NewCRC(payload).Value()

	tampered := append([]byte(nil), payload...)
	tampered = append(tampered, "wrong"...)

	if crc2 := NewCRC(tampered).Value(); crc == crc2 {
		t.Errorf("crc did not change after tampering: both = %d", crc)
	}

	if crc3 := NewCRC([]byte("thisistestcrc32")).Value(); crc != crc3 {
		t.Errorf("crc not deterministic: want = %d, got = %d", crc, crc3)
	}
}

func TestCRC32_Incremental(t *testing.T) {
	whole := NewCRC([]byte("abcd")).Value()
	split := NewCRC([]byte("ab")).Update([]byte("cd")).Value()

	if whole != split {
		t.Errorf("incremental crc mismatch: whole = %d, split = %d", whole, split)
	}
}
