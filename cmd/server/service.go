package main

import (
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/kardianos/service"
)

type program struct {
	exit chan struct{}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) run() {
	logger.Info("Knowledge Server Service started.")
	// Here we will call the main server logic
	runServer()
}

func (p *program) Stop(s service.Service) error {
	logger.Info("Knowledge Server Service stopping.")
	close(p.exit)
	return nil
}

func handleService(cmd string) {
	svcConfig := &service.Config{
		Name:        "knowledge-server",
		DisplayName: "Knowledge Server",
		Description: "MCP Knowledge Server for Codebase and Git Analysis",
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
