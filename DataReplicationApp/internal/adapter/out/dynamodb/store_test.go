package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
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
	s, err := New(ctx, endpoint, "sa-east-1", tm, 4)
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

// O-40 (AP-18, DC-11, DC-12): ScanVersioned com e sem _dr_v, HasItems, e a sobrescrita no
// lugar da cópia: os velhos ficam com a versão da fonte, os iguais não mudam e o mais
// novo não regride. Só roda com DR_IT_DYNAMODB_ENDPOINT definida.
func TestVersionedCopyAgainstLocalStack(t *testing.T) {
	endpoint := os.Getenv("DR_IT_DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DR_IT_DYNAMODB_ENDPOINT não definida")
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const tbl = "dr_copia_{cell}"
	newCell := func(name string) (*Store, string) {
		tm, _ := domain.NewTableMap(fmt.Sprintf("%s%d", name, time.Now().UnixNano()), []string{tbl})
		s, err := New(ctx, endpoint, "sa-east-1", tm, 4)
		if err != nil {
			t.Fatal(err)
		}
		phys, _ := tm.Physical(tbl)
		if _, err := s.client.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:            aws.String(phys),
			AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS}},
			KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash}},
			BillingMode:          types.BillingModePayPerRequest,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(phys)})
		})
		if err := s.LoadCatalog(ctx, 30*time.Second, zerolog.Nop()); err != nil {
			t.Fatal(err)
		}
		return s, phys
	}
	src, srcPhys := newCell("src")
	dst, _ := newCell("dst")

	if has, err := dst.HasItems(ctx, []domain.LogicalTable{tbl}); err != nil || has {
		t.Fatalf("HasItems na tabela vazia = %v %v", has, err)
	}
	put := func(s *Store, pk, nome string, v domain.Index) {
		item := `{"pk":{"S":"` + pk + `"},"nome":{"S":"` + nome + `"}}`
		if err := s.ApplyBatch(ctx, []port.VersionedOp{{Op: domain.Op{Table: tbl, Kind: domain.OpPut,
			Key: []byte(`{"pk":{"S":"` + pk + `"}}`), Item: []byte(item)}, Version: v}}); err != nil {
			t.Fatal(err)
		}
	}
	put(src, "velho", "fonte", 10)
	put(src, "igual", "fonte", 7)
	put(src, "novo", "fonte", 5)
	// Um item sem _dr_v na fonte (gravado por fora): versão 0.
	if _, err := src.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(srcPhys),
		Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "semversao"}}}); err != nil {
		t.Fatal(err)
	}
	put(dst, "velho", "local", 3)
	put(dst, "igual", "local", 7)
	put(dst, "novo", "local", 9)
	if has, err := dst.HasItems(ctx, []domain.LogicalTable{tbl}); err != nil || !has {
		t.Fatalf("HasItems com itens = %v %v", has, err)
	}

	var items []port.VersionedItem
	var start []byte
	for {
		page, err := src.ScanVersioned(ctx, tbl, 2, start)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, page.Items...)
		if page.LastKey == nil {
			break
		}
		start = page.LastKey
	}
	versions := map[string]domain.Index{}
	for _, it := range items {
		if strings.Contains(string(it.Item), VersionAttr) {
			t.Fatalf("_dr_v vazou no item: %s", it.Item)
		}
		var k map[string]map[string]string
		if err := json.Unmarshal(it.Key, &k); err != nil {
			t.Fatalf("chave canônica %s: %v", it.Key, err)
		}
		versions[k["pk"]["S"]] = it.Version
	}
	if len(items) != 4 || versions["velho"] != 10 || versions["igual"] != 7 || versions["novo"] != 5 || versions["semversao"] != 0 {
		t.Fatalf("ScanVersioned = %v", versions)
	}

	for _, it := range items { // a cópia da reconstrução: o mesmo ApplyBatch condicional (AP-04)
		if err := dst.ApplyBatch(ctx, []port.VersionedOp{{Op: domain.Op{Table: tbl, Kind: domain.OpPut, Key: it.Key, Item: it.Item},
			Version: it.Version}}); err != nil {
			t.Fatal(err)
		}
	}
	nome := func(pk string) string {
		b, err := dst.Get(ctx, tbl, []byte(`{"pk":{"S":"`+pk+`"}}`))
		if err != nil || b == nil {
			t.Fatalf("Get %s: %s %v", pk, b, err)
		}
		var m map[string]map[string]string
		_ = json.Unmarshal(b, &m)
		return m["nome"]["S"]
	}
	if nome("velho") != "fonte" || nome("igual") != "local" || nome("novo") != "local" {
		t.Fatalf("sobrescrita no lugar: velho=%s igual=%s novo=%s", nome("velho"), nome("igual"), nome("novo"))
	}
}
