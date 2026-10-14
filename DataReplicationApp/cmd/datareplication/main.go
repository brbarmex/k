// Comando datareplication: só chama o bootstrap e registra por que a partida falhou.
package main

import (
	"context"
	"os"
	"time"

	"example.com/datareplication/internal/bootstrap"
	"example.com/datareplication/internal/platform/logx"
)

func main() {
	log := logx.New()
	app := bootstrap.NewProduction()
	if err := app.Err(); err != nil {
		log.Fatal().Err(err).Msg("a partida falhou")
	}
	start, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()
	if err := app.Start(start); err != nil {
		log.Fatal().Err(err).Msg("a partida falhou")
	}
	sig := <-app.Wait()
	stop, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()
	if err := app.Stop(stop); err != nil {
		log.Error().Err(err).Msg("parada com erro")
	}
	os.Exit(sig.ExitCode)
}
