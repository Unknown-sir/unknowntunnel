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
	"strconv"
	"syscall"

	"github.com/Unknown-sir/Unknowntunnel/internal/admin"
	"github.com/Unknown-sir/Unknowntunnel/internal/app"
	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

var version = "dev"

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		if err := admin.New().Menu(); err != nil {
			log.Printf("error: %v", err)
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "menu", "panel":
		err = admin.New().Menu()
	case "setup", "create":
		err = setupCommand(os.Args[2:])
	case "edit":
		err = editCommand(os.Args[2:])
	case "list":
		err = admin.New().List()
	case "service":
		err = serviceCommand(os.Args[2:])
	case "logs":
		err = logsCommand(os.Args[2:])
	case "show":
		err = showCommand(os.Args[2:])
	case "delete", "remove":
		err = deleteCommand(os.Args[2:])
	case "check-all":
		err = admin.New().ValidateAll()
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

Interactive control panel:
  sudo unknowntunnel
  sudo unknowntunnel menu

Administration:
  sudo unknowntunnel setup  [-instance tunnel1]
  sudo unknowntunnel edit   -instance tunnel1
  sudo unknowntunnel list
  sudo unknowntunnel service -instance tunnel1 -action start|stop|restart|enable|disable|status
  sudo unknowntunnel logs   -instance tunnel1 [-lines 100] [-follow]
  sudo unknowntunnel show   -instance tunnel1
  sudo unknowntunnel delete -instance tunnel1 [-force]
  sudo unknowntunnel check-all

Runtime and diagnostics:
  unknowntunnel run    -config /etc/unknowntunnel/tunnel1.json
  unknowntunnel check  -config /etc/unknowntunnel/tunnel1.json
  unknowntunnel keygen -out /etc/unknowntunnel/secret.key
  unknowntunnel version`)
}

func setupCommand(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return admin.New().Setup(*instance)
}

func editCommand(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("-instance is required")
	}
	return admin.New().Edit(*instance)
}

func serviceCommand(args []string) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	action := fs.String("action", "status", "start, stop, restart, enable, disable, or status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("-instance is required")
	}
	return admin.New().Service(*instance, *action)
}

func logsCommand(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	lines := fs.Int("lines", 100, "number of recent log lines")
	follow := fs.Bool("follow", false, "follow new log messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("-instance is required")
	}
	return admin.New().Logs(*instance, *lines, *follow)
}

func showCommand(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("-instance is required")
	}
	return admin.New().Show(*instance)
}

func deleteCommand(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	instance := fs.String("instance", "", "instance name")
	force := fs.Bool("force", false, "delete without confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("-instance is required")
	}
	return admin.New().Delete(*instance, *force)
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
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	secret := base64.RawURLEncoding.EncodeToString(random)
	if err := config.WriteSecret(*out, secret, *force); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; use -force to replace it", *out)
		}
		return err
	}
	fmt.Printf("secret written to %s (%s characters)\n", *out, strconv.Itoa(len(secret)))
	return nil
}
