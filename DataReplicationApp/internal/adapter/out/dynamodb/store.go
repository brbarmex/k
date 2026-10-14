package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// Store é o banco da célula. Implementa port.StateStore, port.Scanner e port.Catalog.
type Store struct {
	client *dynamodb.Client
	tables domain.TableMap

	mu      sync.RWMutex
	schemas map[domain.LogicalTable]domain.KeySchema
	ready   bool
}

var (
	_ port.StateStore = (*Store)(nil)
	_ port.Scanner    = (*Store)(nil)
	_ port.Catalog    = (*Store)(nil)
)

// minIdleConns é o piso de conexões ociosas guardadas para o banco: leituras e Scans
// usam o mesmo cliente que o applier.
const minIdleConns = 32

// New cria o cliente do DynamoDB. endpoint vazio = AWS real; com valor, o LocalStack.
// applyWorkers dimensiona o pool HTTP: o padrão do SDK guarda só 10 conexões ociosas por
// host, e com mais workers que isso cada gravação a mais abriria conexão nova (ADR-058).
func New(ctx context.Context, endpoint, region string, tm domain.TableMap, applyWorkers int) (*Store, error) {
	idle := max(2*applyWorkers, minIdleConns)
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.MaxIdleConns = idle
		tr.MaxIdleConnsPerHost = idle
	})
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("dynamodb: configuração do SDK: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return &Store{client: client, tables: tm, schemas: map[domain.LogicalTable]domain.KeySchema{}}, nil
}

// ApplyBatch grava cada item com PutItem condicional pela versão (AP-04): o item só é
// escrito se não tem _dr_v ou se a versão gravada é menor. ConditionalCheckFailed
// significa "já aplicado" e não é erro (SG-06).
func (s *Store) ApplyBatch(ctx context.Context, ops []port.VersionedOp) error {
	for _, op := range ops {
		if op.Kind != domain.OpPut {
			return fmt.Errorf("%w: operação %d não suportada nesta fase", port.ErrStorePermanent, op.Kind)
		}
		phys, ok := s.tables.Physical(op.Table)
		if !ok {
			return fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, op.Table)
		}
		item, err := itemFromJSON(op.Item)
		if err != nil {
			return fmt.Errorf("%w: %v", port.ErrStorePermanent, err)
		}
		v := strconv.FormatUint(uint64(op.Version), 10)
		item[VersionAttr] = &types.AttributeValueMemberN{Value: v}
		_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:                 aws.String(phys),
			Item:                      item,
			ConditionExpression:       aws.String("attribute_not_exists(#v) OR #v < :v"),
			ExpressionAttributeNames:  map[string]string{"#v": VersionAttr},
			ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberN{Value: v}},
		})
		var ccf *types.ConditionalCheckFailedException
		if err != nil && !errors.As(err, &ccf) {
			return classify(err)
		}
	}
	return nil
}

// Get lê o item com leitura consistente; não encontrado devolve nil.
func (s *Store) Get(ctx context.Context, table domain.LogicalTable, key []byte) ([]byte, error) {
	phys, ok := s.tables.Physical(table)
	if !ok {
		return nil, fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, table)
	}
	k, err := itemFromJSON(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", port.ErrStorePermanent, err)
	}
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(phys), Key: k, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, classify(err)
	}
	if out.Item == nil {
		return nil, nil
	}
	return itemToJSON(stripVersion(out.Item))
}

// Scan lê uma página da tabela base com leitura consistente, sem _dr_v.
func (s *Store) Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (port.ScanPage, error) {
	phys, ok := s.tables.Physical(table)
	if !ok {
		return port.ScanPage{}, fmt.Errorf("%w: tabela %s fora da lista", port.ErrStorePermanent, table)
	}
	in := &dynamodb.ScanInput{TableName: aws.String(phys), ConsistentRead: aws.Bool(true)}
	if limit > 0 {
		in.Limit = aws.Int32(int32(limit))
	}
	if len(startKey) > 0 {
		k, err := itemFromJSON(startKey)
		if err != nil {
			return port.ScanPage{}, fmt.Errorf("%w: %v", port.ErrStorePermanent, err)
		}
		in.ExclusiveStartKey = k
	}
	out, err := s.client.Scan(ctx, in)
	if err != nil {
		return port.ScanPage{}, classify(err)
	}
	page := port.ScanPage{Items: make([][]byte, 0, len(out.Items)), Scanned: int(out.ScannedCount)}
	for _, it := range out.Items {
		b, err := itemToJSON(stripVersion(it))
		if err != nil {
			return port.ScanPage{}, err
		}
		page.Items = append(page.Items, b)
	}
	if len(out.LastEvaluatedKey) > 0 {
		lk, err := s.canonicalKey(table, out.LastEvaluatedKey)
		if err != nil {
			return port.ScanPage{}, err
		}
		page.LastKey = lk
	}
	return page, nil
}

func (s *Store) canonicalKey(table domain.LogicalTable, key map[string]types.AttributeValue) ([]byte, error) {
	ks, ok := s.KeySchema(table)
	if !ok {
		return itemToJSON(key)
	}
	raw := make(map[string]json.RawMessage, len(key))
	for name, av := range key {
		v, err := avToAny(av)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw[name] = b
	}
	return domain.CanonicalKey(ks, raw)
}

// classify separa o erro do banco em transitório (repetir) e permanente (estacionar o
// applier), AP-07.
func classify(err error) error {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return fmt.Errorf("%w: %v", port.ErrStoreTransient, err) // rede
	}
	switch ae.ErrorCode() {
	case "ProvisionedThroughputExceededException", "ThrottlingException", "RequestLimitExceeded",
		"InternalServerError", "ServiceUnavailable", "LimitExceededException":
		return fmt.Errorf("%w: %v", port.ErrStoreTransient, err)
	default:
		return fmt.Errorf("%w: %v", port.ErrStorePermanent, err)
	}
}
