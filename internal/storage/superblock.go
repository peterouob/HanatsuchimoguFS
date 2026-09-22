package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/peterouob/HanatsuchimoguFS/utils"
)

const (
	MagicSuperblock = 0x48414E41
	FormatVersion   = 1

	SuperblockSize    = 8192
	NeedleStartOffset = SuperblockSize
)

type Superblock struct {
	Magic             uint32
	FormatVersion     uint32
	VolumeID          uint64
	CreatedAtUnixNano int64
	SealedAtUnixNano  int64
}

func NewSuperblock(volumeID uint64) Superblock {
	return Superblock{
		Magic:             MagicSuperblock,
		VolumeID:          volumeID,
		CreatedAtUnixNano: time.Now().UnixNano(),
		FormatVersion:     FormatVersion,
	}
}

func (s Superblock) Sealed() bool {
	return s.SealedAtUnixNano != 0
}

func (s Superblock) Bytes() []byte {
	buf := make([]byte, SuperblockSize)

	crated, err := utils.CIU[int64, uint64](s.CreatedAtUnixNano)
	if err != nil {
		panic(err) // TODO: handle this error
	}

	sealed, err := utils.CIU[int64, uint64](s.SealedAtUnixNano)
	if err != nil {
		panic(err) // TODO: handle this error
	}

	binary.BigEndian.PutUint32(buf[0:4], MagicSuperblock)
	binary.BigEndian.PutUint32(buf[4:8], s.FormatVersion)
	binary.BigEndian.PutUint64(buf[8:16], s.VolumeID)
	binary.BigEndian.PutUint64(buf[16:24], crated)
	binary.BigEndian.PutUint64(buf[24:32], sealed)
	binary.BigEndian.PutUint32(buf[32:36], NeedleStartOffset)

	return buf
}

func WriteSuperblock(w io.WriterAt, s Superblock) error {
	if _, err := w.WriteAt(s.Bytes(), 0); err != nil {
		return fmt.Errorf("write superblock: %w", err)
	}
	return nil
}

func ReadSuperblock(r io.ReaderAt) (Superblock, error) {
	buf := make([]byte, SuperblockSize)

	if _, err := r.ReadAt(buf, 0); err != nil {
		return Superblock{}, fmt.Errorf("read superblock: %w", err)
	}

	if binary.BigEndian.Uint32(buf[0:4]) != MagicSuperblock {
		return Superblock{}, ErrInvalidSuperblock
	}

	crated, err := utils.CUI[uint64, int64](binary.BigEndian.Uint64(buf[16:24]))
	if err != nil {
		return Superblock{}, err
	}

	sealed, err := utils.CUI[uint64, int64](binary.BigEndian.Uint64(buf[24:32]))
	if err != nil {
		return Superblock{}, err
	}

	s := Superblock{
		Magic:             binary.BigEndian.Uint32(buf[0:4]),
		FormatVersion:     binary.BigEndian.Uint32(buf[4:8]),
		VolumeID:          binary.BigEndian.Uint64(buf[8:16]),
		CreatedAtUnixNano: crated,
		SealedAtUnixNano:  sealed,
	}

	start := binary.BigEndian.Uint32(buf[32:36])

	switch {
	case s.Magic != MagicSuperblock:
		return Superblock{}, fmt.Errorf("%w: %d", ErrInvalidSuperblock, s.Magic)
	case s.FormatVersion != FormatVersion:
		return Superblock{}, fmt.Errorf("%w: %d", ErrUnsupportedFormatVersion, s.FormatVersion)
	case start != NeedleStartOffset:
		return Superblock{}, fmt.Errorf("%w: needle start offset %d", ErrInvalidSuperblock, start)
	default:
		return s, nil
	}
}

func SealSuperblock(w io.WriterAt, sealedAtUnixNano int64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(sealedAtUnixNano))

	if _, err := w.WriteAt(buf[:], 24); err != nil {
		return fmt.Errorf("seal superblock: %w", err)
	}
	return nil
}
