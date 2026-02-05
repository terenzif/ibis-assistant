package db

import (
	"context"
	"fmt"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/surrealdb/surrealdb.go" // Standard driver
)

type Executor interface {
	Execute(sql string) (interface{}, error)
	SmartQuery(sql string, vars interface{}) (interface{}, error)
	Close()
}

type Client struct {
	DB      *surrealdb.DB
	Timeout time.Duration
}

// Ensure Client implements Executor
var _ Executor = (*Client)(nil)

func (c *Client) SetTimeout(d time.Duration) {
	c.Timeout = d
}

func NewClient(endpoint, ns, db, user, pass string) (*Client, error) {
	// Connect to SurrealDB
	logger.Info("Connecting to SurrealDB at %s...", endpoint)
	dbConn, err := surrealdb.New(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create surrealdb client: %w", err)
	}

	// Sign in
	if _, err := dbConn.SignIn(context.Background(), map[string]interface{}{
		"user": user,
		"pass": pass,
	}); err != nil {
		return nil, fmt.Errorf("failed to signin: %w", err)
	}

	// Use namespace and database
	if err := dbConn.Use(context.Background(), ns, db); err != nil {
		return nil, fmt.Errorf("failed to use ns/db: %w", err)
	}

	// Default timeout 60s
	return &Client{DB: dbConn, Timeout: 60 * time.Second}, nil
}

func (c *Client) Close() {
	c.DB.Close(context.Background())
}

// Execute performs a raw query against SurrealDB
func (c *Client) Execute(sql string) (interface{}, error) {
	logger.Debug("SQL: %s", sql)

	ctx := context.Background()
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	start := time.Now()
	res, err := surrealdb.Query[interface{}](ctx, c.DB, sql, map[string]interface{}{})
	duration := time.Since(start)
	
	if err != nil {
		logger.Error("SQL ERROR [%v]: %v", duration, err)
		return nil, err
	}
	
	logger.Debug("SQL SUCCESS [%v]", duration)
	
	// Unwrap if single result for backward compatibility
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}

// SmartQuery is a helper for parameterized queries
func (c *Client) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	logger.Debug("SQL (Smart): %s | Vars: %+v", sql, vars)
	varsMap, ok := vars.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("vars must be map[string]interface{}")
	}

	ctx := context.Background()
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	
	start := time.Now()
	res, err := surrealdb.Query[interface{}](ctx, c.DB, sql, varsMap)
	duration := time.Since(start)
	
	if err != nil {
		logger.Error("SQL ERROR [%v]: %v", duration, err)
		return nil, err
	}
	
	logger.Debug("SQL SUCCESS [%v]", duration)

	// Unwrap if single result
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}
