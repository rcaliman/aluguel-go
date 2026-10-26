package main

import (
	"aluguel/internal/app"
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	config := flag.String("config", "config.json", "arquivo de configuração JSON")
	check := flag.Bool("check", false, "verificar conexão e esquema sem iniciar servidor")
	flag.Parse()
	c, e := app.LoadConfig(*config)
	if e != nil {
		log.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, e := app.OpenDatabase(ctx, c)
	if e != nil {
		log.Fatalf("conexão MySQL: %v", e)
	}
	defer db.Close()
	if e = app.ValidateSchema(ctx, db); e != nil {
		log.Fatal(e)
	}
	if *check {
		log.Print("Conexão e esquema compatíveis; nenhuma alteração executada.")
		return
	}
	a, e := app.New(db, c)
	if e != nil {
		log.Fatal(e)
	}
	server := &http.Server{Addr: c.Address, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if e := server.Shutdown(ctx); e != nil {
			log.Print(e)
		}
	}()
	log.Printf("Aluguel em %s", c.Address)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
	<-shutdownDone
}
