package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/yourusername/sharded-cache/internal/cache"
	"github.com/yourusername/sharded-cache/internal/handlers"
)

func initServer(nodes, dirShards, port int) (*http.Server, chan struct{}) {
    c := cache.NewCache(nodes, dirShards)
    stop := make(chan struct{})
    c.StartExpirySweeper(1*time.Second, stop)
    c.StartRebalancer(5*time.Second, stop)

    r := mux.NewRouter()
    r.HandleFunc("/set", handlers.MakeSetHandler(c)).Methods("POST")
    r.HandleFunc("/get/{key}", handlers.MakeGetHandler(c)).Methods("GET")
    r.HandleFunc("/del/{key}", handlers.MakeDelHandler(c)).Methods("DELETE")
    r.HandleFunc("/keys", handlers.MakeKeysHandler(c)).Methods("GET")
    r.HandleFunc("/scan", handlers.MakeScanHandler(c)).Methods("GET")
    r.HandleFunc("/flushall", handlers.MakeFlushHandler(c)).Methods("POST")
    r.HandleFunc("/stats", handlers.MakeStatsHandler(c)).Methods("GET")

    srv := &http.Server{
        Addr:         fmt.Sprintf(":%d", port),
        Handler:      r,
        ReadTimeout:  5 * time.Second,
        WriteTimeout: 10 * time.Second,
        IdleTimeout:  60 * time.Second,
    }
    return srv, stop
}

func main() {
    nodes := 256
    dirShards := 64
    port := 8080

    srv, stop := initServer(nodes, dirShards, port)

    go func() {
        log.Printf("server started at :%d\n", port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen error: %v", err)
        }
    }()

    stopSig := make(chan os.Signal, 1)
    signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
    <-stopSig
    log.Println("shutting down...")

    close(stop)
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Printf("shutdown error: %v", err)
    }
    log.Println("server stopped")
}