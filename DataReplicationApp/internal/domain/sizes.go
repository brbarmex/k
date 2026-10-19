package domain

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ErrBadSize: tamanho em bytes ilegível (negativo, sufixo desconhecido ou estouro).
var ErrBadSize = errors.New("tamanho inválido")

// sizeUnits são os sufixos aceitos por ParseSize: base 1024 (KiB...) e base 1000 (KB...).
// Os mais longos vêm antes, para "MiB" não ser lido como "B".
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
}

// ParseSize lê um tamanho em bytes: um inteiro ("1048576") ou um inteiro com sufixo
// ("64MiB", "2GiB", "64MB"). Usado pelas variáveis de tamanho da compactação (S.6).
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrBadSize
	}
	if n > math.MaxInt64/mult {
		return 0, ErrBadSize
	}
	return n * mult, nil
}
