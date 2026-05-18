package main

import (
	"context"
	"fmt"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/ingest/logs"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

func main() {
	cfg := config.Load("c:\\_dev\\knowledge_server\\dist\\config.json")
	if !cfg.SMTP.Enabled {
		fmt.Println("SMTP is still disabled in config.json")
		return
	}

	logger.Init(cfg.LogLevel, "test_email.log")
	
	dbClient, _ := db.NewSurrealClient(cfg)
	defer dbClient.Close()
	
	reporter := logs.NewReporter(cfg, dbClient)
	reportPath, err := reporter.GenerateReport("DeckOnLine", "seriLog20260430_2.txt", 1, 0.0001)
	if err != nil {
		fmt.Println("Failed to generate report:", err)
		return
	}
	
	fmt.Println("Sending email to", cfg.SMTP.To)
	notifier := logs.NewNotifier(cfg)
	err = notifier.SendEmail(reportPath)
	if err != nil {
		fmt.Println("Failed to send email:", err)
	} else {
		fmt.Println("Email sent successfully!")
	}
}
