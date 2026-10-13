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

func New(extra ...fx.Option) *fx.App {
	return fx.New(
		fx.StartTimeout(60*time.Second), // recuperação do WAL pode demorar
		fx.StopTimeout(25*time.Second),  // < terminationGracePeriodSeconds do pod
		fx.Provide(
			config.Load,
			logx.New,
			func() *domain.Gate { return &domain.Gate{} },
			fx.Annotate(newWriteService, fx.As(new(port.Writer))),
			func(c config.Config) dynamohttp.Deadline { return dynamohttp.Deadline(c.RequestDeadline) },
			dynamohttp.New,
		),
		fx.Invoke(runHTTP),
		fx.Options(extra...), // adaptadores de Raft, WAL e banco entram por módulos
	)
}

func newWriteService(c port.ConsensusEngine, f port.Forwarder, a port.AppliedWaiter, g *domain.Gate) *usecase.WriteService {
	return &usecase.WriteService{Consensus: c, Forward: f, Gate: g, Applied: a, Degraded: func() bool { return false }}
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
