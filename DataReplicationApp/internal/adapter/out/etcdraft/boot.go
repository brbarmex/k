package etcdraft

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.etcd.io/etcd/server/v3/storage/wal"
)

// HasState diz se o diretório já tem um WAL (DC-12): com estado, a partida segue o caminho
// de sempre; sem estado, passa pela decisão do nascimento.
func HasState(dir string) bool { return wal.Exist(dir) }

var tokenRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// LoadBootToken lê o token de partida do volume (DC-12) ou, ausente, gera 128 bits
// aleatórios e grava com tmp, fsync, rename e fsync do diretório, ANTES de a célula
// responder aos pares. Ilegível ou falha de gravação é fatal.
func LoadBootToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		tok := strings.TrimSpace(string(b))
		if !tokenRe.MatchString(tok) {
			return "", fmt.Errorf("etcdraft: token de partida ilegível em %s; rode task clean", path)
		}
		return tok, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("etcdraft: token de partida: %w", err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("etcdraft: token de partida: %w", err)
	}
	tok := hex.EncodeToString(raw)
	if err := writeAtomic(path, []byte(tok+"\n"), "token de partida"); err != nil {
		return "", err
	}
	return tok, nil
}
