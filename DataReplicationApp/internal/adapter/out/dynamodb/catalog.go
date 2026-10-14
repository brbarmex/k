package dynamodb

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
)

// LoadCatalog confere cada tabela permitida no banco da célula e lê o KeySchema dela
// (DescribeTable). Espera até `wait` pela tabela ativa: na POC as tabelas são criadas
// depois de as células subirem (task subir). Tabela ausente no fim do prazo é erro.
func (s *Store) LoadCatalog(ctx context.Context, wait time.Duration, log zerolog.Logger) error {
	deadline := time.Now().Add(wait)
	for _, l := range s.tables.Tables() {
		phys, _ := s.tables.Physical(l)
		for {
			ks, err := s.describe(ctx, phys)
			if err == nil {
				s.mu.Lock()
				s.schemas[l] = ks
				s.mu.Unlock()
				log.Info().Str("tabela", phys).Strs("chave", ks.Names()).Msg("tabela conferida")
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("tabela %s não encontrada em TABLE_WAIT (%s): %v", phys, wait, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

func (s *Store) describe(ctx context.Context, phys string) (domain.KeySchema, error) {
	out, err := s.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(phys)})
	if err != nil {
		return domain.KeySchema{}, err
	}
	if out.Table.TableStatus != types.TableStatusActive {
		return domain.KeySchema{}, fmt.Errorf("tabela %s em %s", phys, out.Table.TableStatus)
	}
	return keySchemaOf(out.Table), nil
}

func keySchemaOf(t *types.TableDescription) domain.KeySchema {
	typeOf := map[string]string{}
	for _, a := range t.AttributeDefinitions {
		typeOf[aws.ToString(a.AttributeName)] = string(a.AttributeType)
	}
	var ks domain.KeySchema
	for _, k := range t.KeySchema {
		attr := domain.KeyAttr{Name: aws.ToString(k.AttributeName), Type: typeOf[aws.ToString(k.AttributeName)]}
		if k.KeyType == types.KeyTypeHash {
			ks.Partition = attr
		} else {
			ks.Sort = attr
		}
	}
	return ks
}

// KeySchema devolve a chave da tabela lógica, se ela foi conferida.
func (s *Store) KeySchema(t domain.LogicalTable) (domain.KeySchema, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks, ok := s.schemas[t]
	return ks, ok
}

// Ready diz se todas as tabelas foram conferidas.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}
