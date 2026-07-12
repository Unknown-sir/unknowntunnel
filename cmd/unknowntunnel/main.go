package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Unknown-sir/Unknowntunnel/internal/app"
	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

var version = "dev"

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCommand(os.Args[2:])
	case "check":
		err = checkCommand(os.Args[2:])
	case "keygen":
		err = keygenCommand(os.Args[2:])
	case "version":
		fmt.Printf("Unknowntunnel %s\n", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Unknowntunnel - encrypted L3/L4 TCP and UDP tunnel

Usage:
  unknowntunnel run    -config /etc/unknowntunnel/client.json
  unknowntunnel check  -config /etc/unknowntunnel/client.json
  unknowntunnel keygen -out /etc/unknowntunnel/secret.key
  unknowntunnel version`)
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to JSON configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("-config is required")
	}
	cfg, secret, err := loadConfigAndSecret(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("starting Unknowntunnel node=%s peer=%s role=%s transport=%s", cfg.NodeID, cfg.PeerID, cfg.Role, cfg.Transport.Mode)
	return app.New(cfg, secret).Run(ctx)
}

func checkCommand(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to JSON configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("-config is required")
	}
	cfg, _, err := loadConfigAndSecret(*configPath)
	if err != nil {
		return err
	}
	fmt.Printf("configuration is valid: node=%s role=%s transport=%s\n", cfg.NodeID, cfg.Role, cfg.Transport.Mode)
	return nil
}

func loadConfigAndSecret(path string) (*config.Config, []byte, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	secret, err := config.ReadSecret(cfg.Auth.SecretFile)
	if err != nil {
		return nil, nil, err
	}
	if info, err := os.Stat(cfg.Auth.SecretFile); err == nil && info.Mode().Perm()&0o077 != 0 {
		log.Printf("warning: secret file %s should have mode 0600", cfg.Auth.SecretFile)
	}
	return cfg, secret, nil
}

func keygenCommand(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "output secret file")
	force := fs.Bool("force", false, "replace an existing secret file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if *force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(*out, flags, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	secret := base64.RawURLEncoding.EncodeToString(random)
	if _, err := fmt.Fprintln(file, secret); err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	fmt.Printf("secret written to %s\n", *out)
	return nil
}
