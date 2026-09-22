package utils

import (
	"errors"
)

var (
	ErrOverflow     = errors.New("convert overflow")
	ErrNoneNegative = errors.New("convert none negative")
)

type NNumber interface {
	int | int8 | int16 | int32 | int64
}

type UNumber interface {
	uint | uint8 | uint16 | uint32 | uint64
}

func CIU[T NNumber, U UNumber](i T) (U, error) {
	var zero U

	if i < 0 {
		return zero, ErrOverflow
	}

	converted := U(i)
	if T(converted) != i {
		return zero, ErrOverflow
	}

	return converted, nil
}

func CUI[T UNumber, N NNumber](i T) (N, error) {
	var zero N
	if i < 0 {
		return zero, ErrOverflow
	}

	converted := N(i)
	if T(converted) != i {
		return zero, ErrOverflow
	}

	return converted, nil
}
