package storage

import (
	"encoding/binary"
	"fmt"

	"github.com/peterouob/HanatsuchimoguFS/utils"
)

const (
	MagicHeader = 0x2DCF25 >> 1
	MagicFooter = 0x2DCF25 << 1
)

const (
	NormalByte byte = iota
	DeleteByte
	TombstoneByte
)

/*
NeedleHeader
+---------------+----------+----------+--------------+------+--------+
| MagicHeader   | Cookie   | Key      | AlternateKey | Flag | Size   |
| 4 bytes       | 8 bytes  | 8 bytes  | 4 bytes      | 1    | 4 bytes|
+---------------+----------+----------+--------------+------+--------+
*/
type NeedleHeader struct {
	Cookie       uint64
	Key          uint64
	MagicHeader  uint32
	AlternateKey uint32
	Size         uint32
	Flag         byte
}

type Needle struct {
	Data   []byte
	Header NeedleHeader
	Footer NeedleFooter
}

/*
	NeedleFooter

+-----------+-------------+
| Checksum  | MagicFooter |
| 4 bytes   | 4 bytes     |
+-----------+-------------+
*/
type NeedleFooter struct {
	Checksum    uint32 // 4
	MagicFooter uint32 // 4
}

const (
	NeedleHeaderSize = 29
	NeedleFooterSize = 8

	maxPayload = xlargeSize - NeedleHeaderSize - NeedleFooterSize
)

func NewNeedle(key uint64, altKey uint32, cookie uint64, data []byte, flag ...byte) *Needle {
	f := NormalByte
	if len(flag) > 0 {
		f = flag[0]
	}

	dataSize, err := utils.CIU[int, uint32](len(data))
	if err != nil {
		panic(err) // TODO: need to handle this error
	}

	return &Needle{
		Header: NeedleHeader{
			Cookie:       cookie,
			Key:          key,
			AlternateKey: altKey,
			MagicHeader:  MagicHeader,
			Size:         dataSize,
			Flag:         f,
		},
		Data:   data,
		Footer: NeedleFooter{MagicFooter: MagicFooter},
	}
}

func (n *Needle) Bytes(bp *BufferPool) (*Buffer, error) {
	totalSize := NeedleHeaderSize + len(n.Data) + NeedleFooterSize

	size, err := utils.CIU[int, uint32](totalSize)
	if err != nil {
		return nil, fmt.Errorf("needle size %d: %w", totalSize, err)
	}

	buf, err := bp.Get(size)
	if err != nil {
		return nil, fmt.Errorf("needle size %d: %w", totalSize, err)
	}

	buf.B = binary.BigEndian.AppendUint32(buf.B, n.Header.MagicHeader)
	buf.B = binary.BigEndian.AppendUint64(buf.B, n.Header.Cookie)
	buf.B = binary.BigEndian.AppendUint64(buf.B, n.Header.Key)
	buf.B = binary.BigEndian.AppendUint32(buf.B, n.Header.AlternateKey)
	buf.B = append(buf.B, n.Header.Flag)
	buf.B = binary.BigEndian.AppendUint32(buf.B, n.Header.Size)

	buf.B = append(buf.B, n.Data...)

	n.Footer.Checksum = NewCRC(buf.B[:NeedleHeaderSize], n.Data).Value()
	buf.B = binary.BigEndian.AppendUint32(buf.B, n.Footer.Checksum)
	buf.B = binary.BigEndian.AppendUint32(buf.B, n.Footer.MagicFooter)

	paddingLen := (8 - (len(buf.B) % 8)) % 8

	if paddingLen > 0 {
		for range paddingLen {
			buf.B = append(buf.B, 0)
		}
	}

	return buf, nil
}

func ValidNeedleBlock(buf []byte, cookie uint64) error {
	if len(buf) < 25 {
		return ErrBufferTooSmall
	}

	if binary.BigEndian.Uint32(buf[:4]) != MagicHeader {
		return ErrMagicNumber
	}

	if binary.BigEndian.Uint64(buf[4:12]) != cookie {
		return ErrCookie
	}

	if buf[24] != NormalByte {
		return ErrDataNotNormal
	}

	return nil
}

func GetNeedleBlockInfo(totalSize, metaSize uint32, buf []byte) ([]byte, error) {
	header := buf[:NeedleHeaderSize]
	data := buf[NeedleHeaderSize : NeedleHeaderSize+metaSize]
	footer := buf[totalSize-NeedleFooterSize:]
	crc := binary.BigEndian.Uint32(footer[0:4])

	if NewCRC(header, data).Value() != crc {
		return nil, ErrCrcNotValid
	}

	if binary.BigEndian.Uint32(footer[4:8]) != MagicFooter {
		return nil, ErrInvalidMagicFooter
	}

	return data, nil
}

func ParseNeedle(buf []byte) *Needle {
	if len(buf) < NeedleHeaderSize+NeedleFooterSize {
		panic(ErrBufferTooSmall)
	}

	size, err := utils.CUI[uint32, int](binary.BigEndian.Uint32(buf[25:29]))
	if err != nil {
		panic(err)
	}

	end := NeedleHeaderSize + size
	if len(buf) < end+NeedleFooterSize {
		panic(ErrBufferTooSmall)
	}

	return &Needle{
		Header: NeedleHeader{
			MagicHeader:  binary.BigEndian.Uint32(buf[0:4]),
			Cookie:       binary.BigEndian.Uint64(buf[4:12]),
			Key:          binary.BigEndian.Uint64(buf[12:20]),
			AlternateKey: binary.BigEndian.Uint32(buf[20:24]),
			Flag:         buf[24],
			Size:         binary.BigEndian.Uint32(buf[25:29]),
		},
		Data: buf[NeedleHeaderSize:end],
		Footer: NeedleFooter{
			Checksum:    binary.BigEndian.Uint32(buf[end : end+4]),
			MagicFooter: binary.BigEndian.Uint32(buf[end+4 : end+8]),
		},
	}
}
