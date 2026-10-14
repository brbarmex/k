// Command benchstore mede o teto do banco da célula SOZINHO (ADR-060, item 4): PutItem
// condicional pela versão, igual ao do applier, com N workers por tabela, durante um
// tempo fixo. Serve para saber quanto o LocalStack (ou outro endpoint) aguenta antes de
// culpar o DataReplication num teste de carga. Uso pela task bench-localstack.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func main() {
	endpoint := flag.String("endpoint", "http://localstack:4566", "endpoint do DynamoDB (vazio = AWS real)")
	region := flag.String("region", "sa-east-1", "região")
	tables := flag.String("tables", "cadastro_cliente_cell_1,cadastro_cliente_cell_2,cadastro_cliente_cell_3", "tabelas, separadas por vírgula")
	workers := flag.Int("workers", 16, "workers por tabela (como APPLY_WORKERS)")
	dur := flag.Duration("duration", 30*time.Second, "duração da medição")
	flag.Parse()

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuração do SDK:", err)
		os.Exit(1)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if *endpoint != "" {
			o.BaseEndpoint = aws.String(*endpoint)
		}
	})

	var (
		ok, fail atomic.Int64
		mu       sync.Mutex
		lat      []time.Duration
		wg       sync.WaitGroup
	)
	run := time.Now().UnixNano()
	deadline := time.Now().Add(*dur)
	for _, t := range strings.Split(*tables, ",") {
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func(table string, w int) {
				defer wg.Done()
				local := make([]time.Duration, 0, 4096)
				for i := 0; time.Now().Before(deadline); i++ {
					v := strconv.Itoa(i + 1)
					pk := fmt.Sprintf("bench-%d-%d-%d", run, w, i)
					t0 := time.Now()
					_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
						TableName: aws.String(table),
						Item: map[string]types.AttributeValue{
							"pk":    &types.AttributeValueMemberS{Value: pk},
							"nome":  &types.AttributeValueMemberS{Value: "Maria"},
							"_dr_v": &types.AttributeValueMemberN{Value: v},
						},
						ConditionExpression:       aws.String("attribute_not_exists(#v) OR #v < :v"),
						ExpressionAttributeNames:  map[string]string{"#v": "_dr_v"},
						ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberN{Value: v}},
					})
					if err != nil {
						fail.Add(1)
						continue
					}
					ok.Add(1)
					local = append(local, time.Since(t0))
				}
				mu.Lock()
				lat = append(lat, local...)
				mu.Unlock()
			}(t, w)
		}
	}
	wg.Wait()

	slices.Sort(lat)
	q := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(float64(len(lat))*p))]
	}
	secs := dur.Seconds()
	nt := len(strings.Split(*tables, ","))
	fmt.Printf("tabelas=%d workers/tabela=%d duração=%s\n", nt, *workers, *dur)
	fmt.Printf("PutItem ok=%d erro=%d  vazão total=%.0f/s  por tabela=%.0f/s\n", ok.Load(), fail.Load(), float64(ok.Load())/secs, float64(ok.Load())/secs/float64(nt))
	fmt.Printf("latência p50=%s p95=%s p99=%s máx=%s\n", q(0.50), q(0.95), q(0.99), q(1))
	fmt.Println("Leitura: a vazão por tabela é o teto do apply de UMA célula. TPS sustentado da POC <= esse valor.")
}
