package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hhh/wxpusher-wecom-bridge/internal/app"
	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
	"github.com/hhh/wxpusher-wecom-bridge/internal/importer"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "run":
		return runCommand(args[1:], stdout, stderr)
	case "import-json":
		return importJSONCommand(args[1:], stdout, stderr)
	case "import-chrome":
		return importChromeCommand(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runCommand(args []string, _ io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to TOML config")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "run requires -config")
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	st, err := openSQLite(cfg.Storage.SQLitePath)
	if err != nil {
		fmt.Fprintf(stderr, "open sqlite: %v\n", err)
		return 1
	}
	defer st.Close()

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.New(cfg, logger, st).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "run bridge: %v\n", err)
		return 1
	}
	return 0
}

func importJSONCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-json", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to TOML config")
	filePath := fs.String("file", "", "path to identity JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || *filePath == "" {
		fmt.Fprintln(stderr, "import-json requires -config and -file")
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	id, err := importer.ImportJSON(*filePath, cfg.WxPusher.Platform, cfg.WxPusher.Version)
	if err != nil {
		fmt.Fprintf(stderr, "import json identity: %v\n", err)
		return 1
	}
	if err := saveImportedIdentity(context.Background(), cfg.Storage.SQLitePath, id); err != nil {
		fmt.Fprintf(stderr, "save identity: %v\n", err)
		return 1
	}
	printImportedIdentity(stdout, id)
	return 0
}

func importChromeCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-chrome", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to TOML config")
	profilePath := fs.String("profile", "", "path to Chrome profile directory")
	extensionID := fs.String("extension-id", "", "WxPusher extension ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || *profilePath == "" || *extensionID == "" {
		fmt.Fprintln(stderr, "import-chrome requires -config, -profile, and -extension-id")
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	id, err := importer.ImportChrome(*profilePath, *extensionID, cfg.WxPusher.Platform, cfg.WxPusher.Version)
	if err != nil {
		fmt.Fprintf(stderr, "import chrome identity: %v\n", err)
		return 1
	}
	if err := saveImportedIdentity(context.Background(), cfg.Storage.SQLitePath, id); err != nil {
		fmt.Fprintf(stderr, "save identity: %v\n", err)
		return 1
	}
	printImportedIdentity(stdout, id)
	return 0
}

func saveImportedIdentity(ctx context.Context, sqlitePath string, id identity.Identity) error {
	st, err := openSQLite(sqlitePath)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.SaveIdentity(ctx, id)
}

func openSQLite(path string) (*store.SQLiteStore, error) {
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
	}
	return store.OpenSQLite(path)
}

func printImportedIdentity(w io.Writer, id identity.Identity) {
	fmt.Fprintf(w, "imported identity source=%s deviceUuid=%s deviceToken=%s pushToken=%s\n",
		id.Source,
		id.DeviceUUID,
		config.RedactSecret(id.DeviceToken),
		config.RedactSecret(id.PushToken),
	)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  wxpusher-bridge run -config /etc/wxpusher-bridge/config.toml
  wxpusher-bridge import-json -config ./config.local.toml -file identity.json
  wxpusher-bridge import-chrome -config ./config.local.toml -profile "/path/to/Profile" -extension-id "<id>"`)
}
