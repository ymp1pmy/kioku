package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/yamd1/kioku/internal/config"
	"github.com/yamd1/kioku/internal/embedding"
	"github.com/yamd1/kioku/internal/server"
	"github.com/yamd1/kioku/internal/storage"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "add" {
		runAdd(os.Args[2:])
		return
	}
	runServer()
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := storage.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	fmt.Fprintln(os.Stderr, "kioku: loading model "+cfg.ModelName+" (初回は自動DLします)...")

	embedder, err := embedding.New(cfg.ModelsDir, cfg.ModelName)
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}
	defer embedder.Close()

	fmt.Fprintln(os.Stderr, "kioku: ready")

	srv := server.New(store, embedder)
	if err := srv.ServeStdio(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	source := fs.String("source", "cli", "source label")
	tagsStr := fs.String("tags", "", "comma-separated tags")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatalf("read stdin: %v", err)
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		log.Fatal("content is empty")
	}

	var tags []string
	if *tagsStr != "" {
		for _, t := range strings.Split(*tagsStr, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := storage.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	fmt.Fprintln(os.Stderr, "kioku: loading model "+cfg.ModelName+" ...")
	embedder, err := embedding.New(cfg.ModelsDir, cfg.ModelName)
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}
	defer embedder.Close()

	emb, err := embedder.Embed(content)
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}

	mem, err := store.Add(content, *source, tags, emb)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	fmt.Printf("保存しました。id: %s\n", mem.ID)
}
