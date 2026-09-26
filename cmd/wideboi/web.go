package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func runWeb(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runWebStatus(cfg, false, stdout)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "status":
		fs := flag.NewFlagSet("wideboi web status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		jsonOut := fs.Bool("json", false, "output JSON")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}
		return runWebStatus(cfg, *jsonOut, stdout)

	case "start":
		fs := flag.NewFlagSet("wideboi web start", flag.ContinueOnError)
		fs.SetOutput(stderr)
		addr := fs.String("addr", "", "listen address (default: configured address or 127.0.0.1:0)")
		fs.StringVar(addr, "a", "", "listen address (shorthand)")
		rotate := fs.Bool("rotate-token", false, "generate a new access token")
		fs.BoolVar(rotate, "r", false, "generate a new access token (shorthand)")
		disableTLS := fs.Bool("disable-tls", false, "disable TLS/HTTPS for web server")
		jsonOut := fs.Bool("json", false, "output JSON")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}
		return runWebStart(cfg, *addr, *rotate, *disableTLS, *jsonOut, stdout)

	case "stop":
		fs := flag.NewFlagSet("wideboi web stop", flag.ContinueOnError)
		fs.SetOutput(stderr)
		jsonOut := fs.Bool("json", false, "output JSON")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}
		return runWebStop(cfg, *jsonOut, stdout)

	case "-h", "--help", "help":
		printWebHelp(stdout)
		return nil

	default:
		// If starts with -, treat as flags for status (e.g. `wideboi web --json`)
		if len(sub) > 0 && sub[0] == '-' {
			fs := flag.NewFlagSet("wideboi web", flag.ContinueOnError)
			fs.SetOutput(stderr)
			jsonOut := fs.Bool("json", false, "output JSON")
			if err := fs.Parse(args); err != nil {
				return err
			}
			return runWebStatus(cfg, *jsonOut, stdout)
		}
		return fmt.Errorf("unknown web subcommand %q (expected start, stop, or status)", sub)
	}
}

func printWebHelp(w io.Writer) {
	fmt.Fprintf(w, `Usage:
  wideboi [flags] web [status] [--json]
                             Show web server status, address, and URL
  wideboi [flags] web start [--addr|-a <addr>] [--rotate-token|-r] [--disable-tls] [--json]
                             Start or update the session web server
  wideboi [flags] web stop [--json]
                             Stop the session web server and disconnect web clients
`)
}

func runWebStatus(cfg config.Config, jsonOut bool, w io.Writer) error {
	req := protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStatus}
	resp, err := rpcQuery[protocol.MsgWebServerControlResponse](cfg, req, 2*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("query web server: %s", resp.Error)
	}

	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if !resp.Running {
		fmt.Fprintln(w, "web server: disabled")
		return nil
	}

	fmt.Fprintln(w, "web server: enabled")
	fmt.Fprintf(w, "listener:   %s\n", resp.Addr)
	if resp.TLSEnabled {
		fmt.Fprintln(w, "tls:        enabled")
	} else {
		fmt.Fprintln(w, "tls:        disabled")
	}
	fmt.Fprintf(w, "url:        %s\n", resp.URL)
	return nil
}

func runWebStart(cfg config.Config, addr string, rotateToken bool, disableTLS bool, jsonOut bool, w io.Writer) error {
	req := protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		Addr:        addr,
		RotateToken: rotateToken,
		DisableTLS:  disableTLS,
	}
	resp, err := rpcQuery[protocol.MsgWebServerControlResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("failed to start web server: %s", resp.Error)
	}

	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Fprintf(w, "wideboi: web client listening at %s\n", resp.URL)
	return nil
}

func runWebStop(cfg config.Config, jsonOut bool, w io.Writer) error {
	req := protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStop}
	resp, err := rpcQuery[protocol.MsgWebServerControlResponse](cfg, req, 3*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("failed to stop web server: %s", resp.Error)
	}

	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Fprintln(w, "wideboi: web server stopped")
	return nil
}
