// Package bootstrap liga adaptadores às portas. É o ÚNICO lugar que importa fx.
package bootstrap

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"example.com/datareplication/internal/adapter/in/dynamohttp"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/platform/config"
	"example.com/datareplication/internal/platform/logx"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// maxWaitingLeader é o teto de requisições esperando uma eleição (ADR-039, consequências).
const maxWaitingLeader = 1000

func New(extra ...fx.Option) *fx.App {
	return fx.New(
		fx.StartTimeout(60*time.Second), // recuperação do WAL pode demorar
		fx.StopTimeout(25*time.Second),  // < terminationGracePeriodSeconds do pod
		fx.Provide(
			config.Load,
			logx.New,
			func() *domain.Gate { return &domain.Gate{} },
			func() *domain.Lease { return &domain.Lease{} },
			func() domain.Clock { return time.Now }, // monotônico; os testes injetam outro
			fx.Annotate(newDegradeService, fx.As(new(usecase.Degrader))),
			fx.Annotate(newWriteService, fx.As(new(port.Writer))),
			func(c config.Config) dynamohttp.Options {
				return dynamohttp.Options{Deadline: dynamohttp.Deadline(c.RequestDeadline), IdleTimeout: c.IdleTimeout}
			},
			dynamohttp.New,
		),
		fx.Invoke(runHTTP),
		fx.Options(extra...), // adaptadores de Raft, WAL e banco entram por módulos
	)
}

// newDegradeService liga a política e o lease da configuração ao estado do shard.
// DEGRADED_POLICY e DEGRADED_LEASE já foram validadas por config.Load.
func newDegradeService(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, l *domain.Lease, clk domain.Clock) (*usecase.DegradeService, error) {
	policy, err := domain.ParseDegradedPolicy(cfg.DegradedPolicy)
	if err != nil {
		return nil, err
	}
	return &usecase.DegradeService{Policy: policy, LeaseDuration: cfg.DegradedLease, Consensus: c, Gate: g, Lease: l, Clock: clk}, nil
}

// newWriteService: o header x-dr-mode vem do estado REAL do shard (o Degrader consulta o
// lease), não de uma função fixa.
func newWriteService(cfg config.Config, c port.ConsensusEngine, f port.Forwarder, a port.AppliedWaiter, g *domain.Gate, d usecase.Degrader) *usecase.WriteService {
	return &usecase.WriteService{
		Consensus: c, Forward: f, Gate: g, Applied: a, Degrade: d,
		WaitLeader: cfg.WaitLeader, DegradeAfter: cfg.DegradeAfter, MaxWaitingLeader: maxWaitingLeader,
	}
}

// runHTTP abre a porta de API só depois que as dependências estão prontas (ordem de OnStart).
func runHTTP(lc fx.Lifecycle, s *dynamohttp.Server, cfg config.Config, log zerolog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := s.ListenAndServe(cfg.APIAddr); err != nil {
					log.Fatal().Err(err).Msg("api caiu")
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error { return s.Shutdown(ctx) },
	})
}
