package db

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/surrealdb/surrealdb.go" // Standard driver
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/terenzif/ibis-arc/internal/logger"
)

type Executor interface {
	Execute(ctx context.Context, sql string) (interface{}, error)
	SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error)
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

	var dbConn *surrealdb.DB
	var err error

	// Custom connection creation to disable internal driver timeouts for WebSockets
	u, parseErr := url.ParseRequestURI(endpoint)
	if parseErr == nil && (u.Scheme == "ws" || u.Scheme == "wss") {
		logger.Info("Detected WebSocket connection, applying custom timeout configuration...")
		conf := connection.NewConfig(u)
		ws := gorillaws.New(conf)
		ws.Timeout = 0 // Disable internal driver timeout, rely on Context

		dbConn, err = surrealdb.FromConnection(context.Background(), ws)
		if err != nil {
			return nil, fmt.Errorf("failed to create custom surrealdb client: %w", err)
		}
	} else {
		// Fallback for HTTP or invalid URL (though New checks it too)
		if strings.HasPrefix(endpoint, "http") {
			logger.Warn("Using HTTP connection. Warning: The driver enforces a 10s timeout on HTTP which cannot be overridden.")
		}
		dbConn, err = surrealdb.New(endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create surrealdb client: %w", err)
		}
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

	// Default timeout 300s (5m)
	return &Client{DB: dbConn, Timeout: 300 * time.Second}, nil
}

func (c *Client) Close() {
	if c == nil || c.DB == nil {
		return
	}
	c.DB.Close(context.Background())
}

// Execute performs a raw query against SurrealDB
func (c *Client) Execute(ctx context.Context, sql string) (interface{}, error) {
	if c == nil || c.DB == nil {
		return nil, fmt.Errorf("database client not initialized")
	}
	logger.Debug("SQL: %s", sql)

	// Wrap caller context with client timeout
	dbCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		dbCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	maxRetries := 3
	var res *[]surrealdb.QueryResult[interface{}]
	var err error
	var duration time.Duration

	for i := 0; i < maxRetries; i++ {
		start := time.Now()
		res, err = surrealdb.Query[interface{}](dbCtx, c.DB, sql, map[string]interface{}{})
		duration = time.Since(start)

		if err == nil {
			logger.Debug("SQL SUCCESS [%v]", duration)
			break
		}

		// Handle cancellation gracefully
		if dbCtx.Err() != nil {
			logger.Debug("SQL Cancelled/Timed Out during execution: %v", dbCtx.Err())
			return nil, dbCtx.Err()
		}

		errStr := err.Error()
		if strings.Contains(strings.ToLower(errStr), "transaction conflict") && i < maxRetries-1 {
			logger.Warn("SQL Transaction Conflict. Retrying (%d/%d)...", i+1, maxRetries)
			time.Sleep(time.Duration(100*(i+1)) * time.Millisecond) // Exponential backoff
			continue
		}

		if strings.Contains(strings.ToLower(errStr), "already exists") {
			logger.Debug("SQL (Already Exists) [%v]: %v", duration, err)
		} else {
			logger.Error("SQL ERROR [%v]: %v", duration, err)
		}
		return nil, err
	}

	// Unwrap if single result for backward compatibility
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}

// SmartQuery is a helper for parameterized queries
func (c *Client) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	if c == nil || c.DB == nil {
		return nil, fmt.Errorf("database client not initialized")
	}
	logger.Debug("SQL (Smart): %s | Vars: %+v", sql, vars)
	varsMap, ok := vars.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("vars must be map[string]interface{}")
	}

	// Wrap caller context with client timeout
	dbCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		dbCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	maxRetries := 3
	var res *[]surrealdb.QueryResult[interface{}]
	var err error
	var duration time.Duration

	for i := 0; i < maxRetries; i++ {
		start := time.Now()
		res, err = surrealdb.Query[interface{}](dbCtx, c.DB, sql, varsMap)
		duration = time.Since(start)

		if err == nil {
			logger.Debug("SQL SUCCESS [%v]", duration)
			break
		}

		// Handle cancellation gracefully
		if dbCtx.Err() != nil {
			logger.Debug("SQL Cancelled/Timed Out during execution: %v", dbCtx.Err())
			return nil, dbCtx.Err()
		}

		errStr := err.Error()
		if strings.Contains(strings.ToLower(errStr), "transaction conflict") && i < maxRetries-1 {
			logger.Warn("SQL Transaction Conflict. Retrying (%d/%d)...", i+1, maxRetries)
			time.Sleep(time.Duration(100*(i+1)) * time.Millisecond) // Exponential backoff
			continue
		}

		logger.Error("SQL ERROR [%v]: %v", duration, err)
		return nil, err
	}

	// Unwrap if single result
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}
