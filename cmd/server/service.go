package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kardianos/service"
	"github.com/terenzif/ibis-assistant/internal/logger"
)

type program struct {
	cancel context.CancelFunc
}

func (p *program) Start(s service.Service) error {
	// Create a context that can be cancelled to stop the server
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.run(ctx)
	return nil
}

func (p *program) run(ctx context.Context) {
	logger.Info("Ibis Assistant Service started.")
	// Here we will call the main server logic, passing the context
	runServer(ctx)
}

func (p *program) Stop(s service.Service) error {
	logger.Info("Ibis Assistant Service stopping.")
	// Signal the server to stop
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func shouldRunWizard() bool {
	interactive := service.Interactive()
	arg1 := ""
	if len(os.Args) >= 2 {
		arg1 = os.Args[1]
	}
	skip := false
	switch {
	case !interactive:
		skip = true
	case isPluginLaunch():
		skip = true
	case configExists():
		skip = true
	case arg1 == "help", arg1 == "-h", arg1 == "--help",
		arg1 == "install", arg1 == "uninstall", arg1 == "/install", arg1 == "/uninstall",
		arg1 == "/run", arg1 == "start", arg1 == "stop", arg1 == "config":
		skip = true
	}
	return !skip
}

func handleService(cmd string) {
	svcConfig := &service.Config{
		Name:        "ibis-assistant",
		DisplayName: "Ibis Assistant",
		Description: "MCP Ibis Assistant for Codebase and Git Analysis",
		Arguments:   []string{"/run"}, // When running as service, use /run
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Printf("Error creating service: %v\n", err)
		return
	}

	switch cmd {
	case "/install":
		err = s.Install()
		if err != nil {
			fmt.Printf("Failed to install service: %v\n", err)
		} else {
			fmt.Println("Service installed successfully.")
		}
	case "/uninstall":
		err = s.Uninstall()
		if err != nil {
			fmt.Printf("Failed to uninstall service: %v\n", err)
		} else {
			fmt.Println("Service uninstalled successfully.")
		}
	case "/run":
		err = s.Run()
		if err != nil {
			fmt.Printf("Failed to run service: %v\n", err)
		}
	}
}
