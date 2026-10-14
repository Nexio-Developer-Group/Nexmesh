package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/app"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalogdata"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
)

// version is set at release build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	local := flag.Bool("local", false, "single-user local mode: simulated runtime, data saved under your user profile, opens the browser")
	noBrowser := flag.Bool("no-browser", false, "with -local: do not open the browser")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("nexmesh", version)
		return
	}

	var openURL string
	if *local {
		u, err := config.ApplyLocal()
		if err != nil {
			fatal(err)
		}
		openURL = u
		if err := resolveCatalog(); err != nil {
			fatal(err)
		}
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log, app.Options{WithUI: true})
	if err != nil {
		log.Error("startup failed", "err", err)
		if *local {
			pause()
		}
		os.Exit(1)
	}
	defer a.Close()
	a.Start(ctx)

	srv := &http.Server{Addr: cfg.Addr, Handler: a.Handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("control plane listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	if *local {
		fmt.Printf("\n  Nexmesh %s is running at %s\n  Your data is in %s\n  Close this window (or press Ctrl+C) to stop.\n\n", version, openURL, config.DataDir())
		if !*noBrowser {
			go func() { time.Sleep(800 * time.Millisecond); openBrowser(openURL) }()
		}
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

// resolveCatalog points NEXMESH_CATALOG_DIR at a catalog: the one the user
// configured, one next to the executable, or the copy embedded in the binary.
func resolveCatalog() error {
	if os.Getenv("NEXMESH_CATALOG_DIR") != "" {
		return nil
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Join(filepath.Dir(exe), "catalog")
		if st, err := os.Stat(filepath.Join(d, "components")); err == nil && st.IsDir() {
			return os.Setenv("NEXMESH_CATALOG_DIR", d)
		}
	}
	if catalogdata.Available() {
		d := filepath.Join(config.DataDir(), "catalog")
		if err := catalogdata.Extract(d); err != nil {
			return fmt.Errorf("unpack catalog: %w", err)
		}
		return os.Setenv("NEXMESH_CATALOG_DIR", d)
	}
	return nil // fall back to ../catalog (source checkout)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Nexmesh could not start:", err)
	pause()
	os.Exit(1)
}

// pause keeps a double-clicked console window open long enough to read an error.
func pause() {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "\nPress Enter to close.")
		_, _ = fmt.Fscanln(os.Stdin)
	}
}
