package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

func TestConvertRoundTrip(t *testing.T) {
	in := `{"pk":{"S":"cli-1"},"n":{"N":"12.5"},"b":{"B":"AQI="},"ok":{"BOOL":true},"z":{"NULL":true},` +
		`"ss":{"SS":["a","b"]},"ns":{"NS":["1"]},"bs":{"BS":["AQ=="]},"m":{"M":{"x":{"S":"y"}}},"l":{"L":[{"N":"1"},{"S":"s"}]}}`
	item, err := itemFromJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := itemToJSON(item)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(in), &a)
	_ = json.Unmarshal(out, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("ida e volta mudou o item:\n%s\n%s", in, out)
	}
	for _, bad := range []string{`{"a":{"S":"x","N":"1"}}`, `{"a":{"X":"1"}}`, `{"a":{}}`, `[1]`} {
		if _, err := itemFromJSON([]byte(bad)); err == nil {
			t.Fatalf("devia recusar %s", bad)
		}
	}
}

type apiErr struct{ code string }

func (e apiErr) Error() string                 { return e.code }
func (e apiErr) ErrorCode() string             { return e.code }
func (e apiErr) ErrorMessage() string          { return e.code }
func (e apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestClassify(t *testing.T) {
	for code, want := range map[string]error{
		"ThrottlingException":                    port.ErrStoreTransient,
		"ProvisionedThroughputExceededException": port.ErrStoreTransient,
		"InternalServerError":                    port.ErrStoreTransient,
		"ValidationException":                    port.ErrStorePermanent,
		"ResourceNotFoundException":              port.ErrStorePermanent,
	} {
		if err := classify(apiErr{code}); !errors.Is(err, want) {
			t.Fatalf("%s → %v", code, err)
		}
	}
	if err := classify(errors.New("connection refused")); !errors.Is(err, port.ErrStoreTransient) {
		t.Fatalf("erro de rede → %v", err)
	}
}

// Teste contra o LocalStack (O-05). Só roda com DR_IT_DYNAMODB_ENDPOINT definida.
func TestStoreAgainstLocalStack(t *testing.T) {
	endpoint := os.Getenv("DR_IT_DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DR_IT_DYNAMODB_ENDPOINT não definida")
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cell := fmt.Sprintf("it%d", time.Now().UnixNano())
	tm, _ := domain.NewTableMap(cell, []string{"dr_teste_{cell}"})
	s, err := New(ctx, endpoint, "sa-east-1", tm)
	if err != nil {
		t.Fatal(err)
	}
	phys, _ := tm.Physical("dr_teste_{cell}")
	_, err = s.client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(phys),
		AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS}},
		KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash}},
		BillingMode:          types.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(phys)})
	if err := s.LoadCatalog(ctx, 30*time.Second, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	if ks, ok := s.KeySchema("dr_teste_{cell}"); !ok || ks.Partition != (domain.KeyAttr{Name: "pk", Type: "S"}) || ks.HasSort() {
		t.Fatalf("KeySchema = %+v %v", ks, ok)
	}

	key := []byte(`{"pk":{"S":"a"}}`)
	put := func(v domain.Index, nome string) {
		op := port.VersionedOp{Op: domain.Op{Table: "dr_teste_{cell}", Kind: domain.OpPut, Key: key,
			Item: []byte(`{"pk":{"S":"a"},"nome":{"S":"` + nome + `"}}`)}, Version: v}
		if err := s.ApplyBatch(ctx, []port.VersionedOp{op}); err != nil {
			t.Fatalf("apply %d: %v", v, err)
		}
	}
	put(5, "cinco")
	put(3, "tres") // versão menor: "já aplicado", não regride (SG-06)
	put(5, "cinco")

	got, err := s.Get(ctx, "dr_teste_{cell}", key)
	if err != nil || string(got) != `{"nome":{"S":"cinco"},"pk":{"S":"a"}}` {
		t.Fatalf("Get = %s %v", got, err)
	}
	page, err := s.Scan(ctx, "dr_teste_{cell}", 0, nil)
	if err != nil || len(page.Items) != 1 || string(page.Items[0]) != string(got) {
		t.Fatalf("Scan = %+v %v", page, err)
	}
	raw, _ := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(phys), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "a"}}})
	if v, ok := raw.Item[VersionAttr].(*types.AttributeValueMemberN); !ok || v.Value != "5" {
		t.Fatalf("_dr_v no banco = %v", raw.Item[VersionAttr])
	}
	if miss, err := s.Get(ctx, "dr_teste_{cell}", []byte(`{"pk":{"S":"nao"}}`)); err != nil || miss != nil {
		t.Fatalf("item ausente = %s %v", miss, err)
	}
}
