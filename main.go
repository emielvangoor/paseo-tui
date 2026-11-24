package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	host := flag.String("host", "", "daemon endpoint (host:port, tcp://, ssh, or pairing URL), e.g. 192.168.1.20:6767")
	home := flag.String("home", "", "local daemon home (default ~/.paseo)")
	node := flag.String("node", "", "JavaScript runtime for the bridge (default: node on PATH, else Paseo's built-in runtime)")
	bridge := flag.String("bridge", "", "path to paseo-bridge.mjs (default: the copy embedded in this binary)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println("paseo-tui", version)
		return
	}
	nodePath, nodeEnv, err := resolveNode(*node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "paseo-tui:", err)
		os.Exit(1)
	}
	script, err := resolveBridge(*bridge)
	if err != nil {
		fmt.Fprintln(os.Stderr, "paseo-tui: bridge:", err)
		os.Exit(1)
	}
	cfg := BridgeConfig{Node: nodePath, Env: nodeEnv, Script: script, HostLabel: "local"}
	switch {
	case *host != "":
		cfg.Args, cfg.HostLabel = []string{"--host", *host}, *host
	case *home != "":
		cfg.Args, cfg.HostLabel = []string{"--home", *home}, *home
	}

	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "paseo-tui:", err)
		os.Exit(1)
	}
}
