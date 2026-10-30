// Mensaplan Zürich: the menus of the ETH, UZH and other canteens in Zurich (and Basel), on one page.
//
// This file wires the parts together:
//
//	catalog   which canteens exist and where their menus come from (canteens.json)
//	eth       reads menus from ETH's menu API
//	food2050  reads menus from app.food2050.ch (UZH and other ZFV canteens)
//	refresh   asks both sources for new menus every hour and when canteens open
//	menu      the menu data model and the store that keeps it (saved as data/menus.json)
//	photos    shrinks and caches dish photos
//	web       the website itself
//
// Configuration comes from environment variables, all optional:
//
//	ADDR          where to listen (default ":8080")
//	DATA_DIR      where menus and photo thumbnails are kept (default "./data")
//	DOMAIN        the public domain, e.g. "mensa.miszori.dev", for absolute links in link
//	              previews (default: the address of each request)
//
// The release version is in the file VERSION (e.g. "1.0.0"), embedded at build time; each
// release is tagged with it on GitHub (v1.0.0). It is logged at start and shown by /healthz.
//
// "mensaplan healthcheck" asks a running instance whether it is up and prints its status,
// including sources whose menus are stale (used by Docker, whose minimal image has no curl).
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // include the time zone database, so Europe/Zurich works in any container

	"mensaplan/internal/catalog"
	"mensaplan/internal/eth"
	"mensaplan/internal/food2050"
	"mensaplan/internal/menu"
	"mensaplan/internal/photos"
	"mensaplan/internal/refresh"
	"mensaplan/internal/web"
)

//go:embed VERSION
var version string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(env("ADDR", ":8080")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	addr := env("ADDR", ":8080")
	dataDir := env("DATA_DIR", "./data")
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		return err
	}
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	store, err := menu.Open(filepath.Join(dataDir, "menus.json"))
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	ethClient := eth.New(client, eth.BaseURL)

	// Stop cleanly on Ctrl-C or when Docker stops the container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	photoHandler := &photos.Handler{
		Dir: filepath.Join(dataDir, "photos"), Lookup: store.PhotoURL, HTTP: client,
		Allowed:     []string{eth.BaseURL + "/images/", food2050.PhotoURL},
		LogoSources: []string{eth.BaseURL + "/images/"},
	}

	// Menus are refreshed in the background; the site serves what the store has meanwhile.
	refresher := &refresh.Refresher{
		Catalog: cat, Store: store, Zone: zurich, Log: log,
		ETH: ethClient, Food2050: food2050.New(client), Photos: photoHandler,
	}
	refreshDone := make(chan struct{})
	go func() {
		refresher.Run(ctx)
		close(refreshDone)
	}()

	site := &web.Server{
		Catalog: cat, Store: store, Zone: zurich, Log: log, Photos: photoHandler,
		Domain: os.Getenv("DOMAIN"), Release: strings.TrimSpace(version),
		// Menus count as stale after six missed hourly refreshes.
		StaleAfter: 6 * time.Hour,
	}
	server := &http.Server{
		Addr: addr, Handler: site.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second, // a photo's first request downloads it (up to 30s)
		IdleTimeout:       2 * time.Minute,
		// The site reads no cookies or other large headers; Go's default allows 1 MB.
		MaxHeaderBytes: 32 << 10,
	}

	// On Ctrl-C or "docker stop": stop accepting requests, let running ones finish (up to
	// 5 seconds; Docker waits 10 before killing), and let a running refresh save its work.
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		close(shutdownDone)
	}()

	log.Info("Mensaplan Zürich listening", "version", strings.TrimSpace(version), "addr", addr, "data", dataDir)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ListenAndServe returns as soon as shutting down starts; wait for it to finish.
	<-shutdownDone
	select {
	case <-refreshDone:
	case <-time.After(3 * time.Second):
		log.Warn("refresh still running at shutdown")
	}
	log.Info("stopped")
	return nil
}

// healthcheck succeeds if the server listening on addr answers /healthz.
func healthcheck(addr string) error {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(os.Stdout, resp.Body) // the status: last refreshes and stale sources
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
