// Command sloplex runs the Sloplex media server.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/config"
	"github.com/mtdewwolf/sloplex/internal/gdm"
	"github.com/mtdewwolf/sloplex/internal/identity"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	configPath := flag.String("config", "", "path to the JSON config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	port, _ := cfg.Port()
	networks, _ := cfg.Networks()

	id, created, err := identity.Load(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	if created {
		log.Info("generated server identity; use this token in the X-Plex-Token header",
			"token", id.Token, "file", filepath.Join(cfg.DataDir, "identity.json"))
	}

	store, err := library.Open(filepath.Join(cfg.DataDir, "library.json"))
	if err != nil {
		return err
	}
	defs := make([]library.SectionDef, len(cfg.Libraries))
	for i, l := range cfg.Libraries {
		defs[i] = library.SectionDef{Title: l.Title, Type: l.Type, Paths: l.Paths}
	}
	if err := store.SyncSections(defs, time.Now()); err != nil {
		return err
	}

	play, err := playstate.Open(filepath.Join(cfg.DataDir, "playstate.json"))
	if err != nil {
		return err
	}
	accts, err := accounts.Open(filepath.Join(cfg.DataDir, "accounts.json"))
	if err != nil {
		return err
	}

	var prober probe.Prober = probe.None{}
	if cfg.FFprobe != "-" {
		if p, err := probe.NewFFprobe(cfg.FFprobe); err != nil {
			log.Warn("ffprobe not available; media details will be limited", "err", err)
		} else {
			prober = p
		}
	}

	ffmpeg := ""
	if cfg.FFmpeg != "-" {
		bin := cmp.Or(cfg.FFmpeg, "ffmpeg")
		if p, err := exec.LookPath(bin); err != nil {
			log.Warn("ffmpeg not available; episodes without artwork get no thumbnail", "err", err)
		} else {
			ffmpeg = p
		}
	}
	art := artwork.New(filepath.Join(cfg.DataDir, "cache"), ffmpeg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scanner := library.NewScanner(store, prober, log)
	defer scanner.Wait()
	for _, sec := range store.Sections() {
		scanner.Start(ctx, sec.ID)
	}

	srv := server.New(ctx, server.Options{
		FriendlyName:    cfg.FriendlyName,
		MachineID:       id.MachineID,
		Version:         version,
		Tokens:          append([]string{id.Token}, cfg.Tokens...),
		AllowedNetworks: networks,
	}, server.Deps{Store: store, Scanner: scanner, Play: play, Art: art, Accounts: accts, Log: log})

	started := time.Now().Unix()
	if cfg.Discovery {
		conn, err := gdm.Listen(log)
		if err != nil {
			log.Warn("gdm: discovery disabled", "err", err)
		} else {
			go func() {
				info := func() gdm.Announcement {
					return gdm.Announcement{MachineID: id.MachineID, Name: cfg.FriendlyName,
						Version: version, Port: port, UpdatedAt: started}
				}
				if err := gdm.Serve(ctx, conn, info, log); err != nil {
					log.Error("gdm stopped", "err", err)
				}
			}()
		}
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "name", cfg.FriendlyName, "machineIdentifier", id.MachineID)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
