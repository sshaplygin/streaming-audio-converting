package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

var fileBuffer bytes.Buffer

const filename = "source.wav"
const name = "source"
const filetype = "audio/wav"

func chunkedHandler(w http.ResponseWriter, r *http.Request) {
}

func serveContent(w http.ResponseWriter, r *http.Request) {
	file, err := http.Dir(".").Open(filename)
	if err != nil {
		http.Error(w, fmt.Sprintf("unable to open file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	fstat, err := file.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("unable to get file stats: %v", err), http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, name, fstat.ModTime(), file)
}

func main() {
	ctx, cancel := WithCancel(context.Background())
	defer cancel()

	defer func() {
		if p := recover(); p != nil {
			log.Println("shit happend. recovered panic:", p, string(debug.Stack()))
		}
	}()

	file, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

	n, err := io.Copy(&fileBuffer, file)
	if err != nil {
		log.Panic(err)
	}

	log.Println("read bytes from source file:", n)

	mux := http.NewServeMux()

	mux.HandleFunc("/chunked", chunkedHandler)
	mux.HandleFunc("/serve-content", serveContent)

	server := &http.Server{
		Handler: mux,
		Addr:    "127.0.0.1:18090",
	}

	go func() {
		log.Println("server was run")

		if err := server.ListenAndServe(); err != nil {
			log.Println("server down:", err)
		}
	}()

	<-ctx.Done()

	shutdown(server)

	log.Println("server was shutdown")
}

func shutdown(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Println("shutdown: ", err)
	}
}

func WithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	return notifyContext(parent, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
}

func notifyContext(parent context.Context, signals ...os.Signal) (ctx context.Context, stop context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, signals...)
	if ctx.Err() == nil {
		go func() {
			select {
			case <-ch:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, func() {
		cancel()
		signal.Stop(ch)
	}
}
