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
	"runtime"
	"syscall"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/config"
	"github.com/mtdewwolf/sloplex/internal/gdm"
	"github.com/mtdewwolf/sloplex/internal/identity"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playlists"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/preferences"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/server"
	"github.com/mtdewwolf/sloplex/internal/subtitles"
	"github.com/mtdewwolf/sloplex/internal/transcode"
	"github.com/mtdewwolf/sloplex/internal/truenas"
	"github.com/mtdewwolf/sloplex/internal/updates"
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
	// The logDebug preference may change the level while running.
	level := new(slog.LevelVar)
	if *debug {
		level.Set(slog.LevelDebug)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := run(*configPath, *debug, level, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, debug bool, level *slog.LevelVar, log *slog.Logger) error {
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

	interval, _ := cfg.ScanEvery() // checked by Validate
	// Stored preferences override these config values; see
	// docs/spec/preferences.md.
	prefs, err := preferences.Open(filepath.Join(cfg.DataDir, "preferences.json"), preferences.Bootstrap{
		FriendlyName: cfg.FriendlyName, ScanInterval: interval, Watch: cfg.Watch, Discovery: cfg.Discovery, Debug: debug,
	})
	if err != nil {
		return err
	}

	store, err := library.Open(filepath.Join(cfg.DataDir, "library.json"))
	if err != nil {
		return err
	}
	defs := make([]library.SectionDef, len(cfg.Libraries))
	for i, l := range cfg.Libraries {
		defs[i] = library.SectionDef{ConfigID: l.ID, Title: l.Title, Type: l.Type, Paths: l.Paths}
	}
	if err := store.SyncSections(defs, time.Now()); err != nil {
		return err
	}

	lists, err := playlists.Open(filepath.Join(cfg.DataDir, "playlists.json"))
	if err != nil {
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
	nas, err := truenas.Open(filepath.Join(cfg.DataDir, "truenas.json"))
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
	go preferences.FollowLogLevel(ctx, prefs, level)

	var hw transcode.HW
	if ffmpeg != "" {
		hw = transcode.Detect(ctx, ffmpeg, cfg.Hwaccel, cfg.HwaccelDevice, runtime.GOOS, log)
		hw.ToneMap = transcode.DetectToneMap(ctx, ffmpeg, hw, cfg.GPUToneMapping, log)
	}
	tc, err := transcode.New(transcode.Options{
		FFmpeg: ffmpeg, Dir: filepath.Join(cfg.DataDir, "transcode"), MaxTranscodes: cfg.MaxTranscodes,
		HW: hw, Requested: cfg.Hwaccel, Preset: cfg.X264Preset, Throttle: cfg.TranscodeThrottle, Log: log,
	})
	if err != nil {
		return err
	}
	defer tc.Close()
	if ffmpeg != "" && cfg.X264Preset == transcode.PresetAuto {
		// Software transcodes use the default preset until this finishes.
		go func() { tc.SetCapacity(transcode.Benchmark(ctx, ffmpeg, log)) }()
	}

	scanner := library.NewScanner(store, prober, log)
	defer scanner.Wait()
	for _, sec := range store.Sections() {
		scanner.Start(ctx, sec.ID)
	}
	// Interval scans follow the preferences at runtime; watching is set up
	// once.
	scanner.Auto(ctx, library.AutoOptions{Watch: prefs.Watch()})
	scheduleDone := make(chan struct{})
	go func() {
		defer close(scheduleDone)
		preferences.RunScanSchedule(ctx, prefs, func() {
			for _, sec := range store.Sections() {
				scanner.Start(ctx, sec.ID)
			}
		})
	}()
	// Runs before scanner.Wait, so no scan starts while it waits.
	defer func() { stop(); <-scheduleDone }()
	checker := updates.New(version, cfg.UpdateURL, log)
	if cfg.UpdateCheck {
		go checker.Run(ctx, time.Minute, 24*time.Hour)
	}

	if cfg.StrictTokenAuth {
		// See "Strict token mode" in docs/spec/auth.md.
		log.Info("strictTokenAuth is obsolete: invalid tokens are always refused; remove it from the config")
	}
	srv := server.New(ctx, server.Options{
		FriendlyName:    cfg.FriendlyName,
		MachineID:       id.MachineID,
		Version:         version,
		Tokens:          append([]string{id.Token}, cfg.Tokens...),
		AllowedNetworks: networks,
		AutoUpdateCheck: cfg.UpdateCheck,
	}, server.Deps{Store: store, Scanner: scanner, Play: play, Playlists: lists, Art: art, Accounts: accts, Transcoder: tc,
		Updates: checker, TrueNAS: nas, Prefs: prefs,
		Subtitles: subtitles.New(ffmpeg, filepath.Join(cfg.DataDir, "cache", "subs")), Log: log})

	started := time.Now().Unix()
	announced := make(chan struct{})
	if !prefs.Discovery() {
		close(announced)
	} else {
		conn, err := gdm.Listen(log)
		if err != nil {
			log.Warn("gdm: discovery disabled", "err", err)
			close(announced)
		} else {
			info := gdmInfo(prefs, id.MachineID, port, started)
			go func() {
				if err := gdm.Serve(ctx, conn, info, log); err != nil {
					log.Error("gdm stopped", "err", err)
				}
			}()
			go func() {
				defer close(announced)
				gdm.Announce(ctx, info, gdm.AnnounceTargets, log)
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
		log.Info("listening", "addr", cfg.Listen, "name", prefs.FriendlyName(), "machineIdentifier", id.MachineID)
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
	err = httpSrv.Shutdown(shutdownCtx)
	<-announced // BYE
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// gdmInfo describes the server for GDM, with the name in effect at each
// call.
func gdmInfo(prefs *preferences.Store, machineID string, port int, started int64) func() gdm.Announcement {
	return func() gdm.Announcement {
		return gdm.Announcement{MachineID: machineID, Name: prefs.FriendlyName(),
			Version: version, Port: port, UpdatedAt: started}
	}
}
