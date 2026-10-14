// Package memstore é um banco da célula em memória, SÓ DA POC (STORE=memory, ADR-060).
// Serve para medir o DataReplication em teste de carga sem o LocalStack no caminho: os
// dados somem no reinício, e a configuração recusa STORE=memory fora da POC.
//
// Mesma semântica do adaptador DynamoDB onde importa ao núcleo: escrita condicional pela
// versão (só grava versão maior; versão igual ou menor é "já aplicado", sem erro), leitura
// sem _dr_v, Scan paginado pela chave canônica e só PutItem nesta fase.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// KeySchema das tabelas em memória: a POC só tem tabelas com chave de partição pk (S).
var KeySchema = domain.KeySchema{Partition: domain.KeyAttr{Name: "pk", Type: "S"}}

type record struct {
	item    []byte
	version domain.Index
}

// Store implementa port.StateStore, port.Scanner e port.Catalog em memória.
type Store struct {
	tables  domain.TableMap
	latency time.Duration // espera por item gravado (MEMSTORE_LATENCY)

	mu   sync.RWMutex
	data map[domain.LogicalTable]map[string]record
}

var (
	_ port.StateStore = (*Store)(nil)
	_ port.Scanner    = (*Store)(nil)
	_ port.Catalog    = (*Store)(nil)
)

// New cria o banco vazio com as tabelas da configuração.
func New(tm domain.TableMap, latency time.Duration) *Store {
	s := &Store{tables: tm, latency: latency, data: map[domain.LogicalTable]map[string]record{}}
	for _, t := range tm.Tables() {
		s.data[t] = map[string]record{}
	}
	return s
}

// LoadCatalog não espera nada: as tabelas existem desde New.
func (s *Store) LoadCatalog(_ context.Context, _ time.Duration, log zerolog.Logger) error {
	log.Warn().Msg("STORE=memory: banco em memória, só para teste de carga da POC; os dados somem no reinício")
	return nil
}

func (s *Store) KeySchema(t domain.LogicalTable) (domain.KeySchema, bool) {
	_, ok := s.data[t]
	return KeySchema, ok
}

func (s *Store) Ready() bool { return true }

// ApplyBatch grava cada item se a versão for maior que a gravada (AP-04, SG-06).
func (s *Store) ApplyBatch(ctx context.Context, ops []port.VersionedOp) error {
	for _, op := range ops {
		if op.Kind != domain.OpPut {
			return fmt.Errorf("%w: operação %d não suportada nesta fase", port.ErrStorePermanent, op.Kind)
		}
		if s.latency > 0 {
			t := time.NewTimer(s.latency)
			select {
			case <-ctx.Done():
				t.Stop()
				return fmt.Errorf("%w: %v", port.ErrStoreTransient, ctx.Err())
			case <-t.C:
			}
		}
		s.mu.Lock()
		tbl, ok := s.data[op.Table]
		if !ok {
			s.mu.Unlock()
			return fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, op.Table)
		}
		k := string(op.Key)
		if cur, ok := tbl[k]; !ok || cur.version < op.Version {
			tbl[k] = record{item: append([]byte(nil), op.Item...), version: op.Version}
		}
		s.mu.Unlock()
	}
	return nil
}

// Get devolve o item (sem _dr_v, que aqui nunca é gravado no item) ou nil.
func (s *Store) Get(_ context.Context, table domain.LogicalTable, key []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tbl, ok := s.data[table]
	if !ok {
		return nil, fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, table)
	}
	r, ok := tbl[string(key)]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), r.item...), nil
}

// Scan devolve uma página em ordem de chave canônica, depois de startKey.
func (s *Store) Scan(_ context.Context, table domain.LogicalTable, limit int, startKey []byte) (port.ScanPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tbl, ok := s.data[table]
	if !ok {
		return port.ScanPage{}, fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, table)
	}
	keys := make([]string, 0, len(tbl))
	for k := range tbl {
		if len(startKey) == 0 || k > string(startKey) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var page port.ScanPage
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
		page.LastKey = []byte(keys[len(keys)-1])
	}
	page.Items = make([][]byte, 0, len(keys))
	for _, k := range keys {
		page.Items = append(page.Items, append([]byte(nil), tbl[k].item...))
	}
	page.Scanned = len(page.Items)
	return page, nil
}
