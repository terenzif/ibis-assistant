package db

import (
	"context"
	"fmt"
	"log"

	"github.com/surrealdb/surrealdb.go" // Standard driver
)

type Client struct {
	DB *surrealdb.DB
}

func NewClient(endpoint, ns, db, user, pass string) (*Client, error) {
	// Connect to SurrealDB
	log.Printf("Connecting to SurrealDB at %s...", endpoint)
	dbConn, err := surrealdb.New(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create surrealdb client: %w", err)
	}

	// Sign in
	// v1.0.0: SignIn (CamelCase), requires context
	if _, err := dbConn.SignIn(context.Background(), map[string]interface{}{
		"user": user,
		"pass": pass,
	}); err != nil {
		return nil, fmt.Errorf("failed to signin: %w", err)
	}

	// Use namespace and database
	// v1.0.0: Use(ctx, ns, db)
	if err := dbConn.Use(context.Background(), ns, db); err != nil {
		return nil, fmt.Errorf("failed to use ns/db: %w", err)
	}

	return &Client{DB: dbConn}, nil
}

func (c *Client) Close() {
	// v1.0.0: Close(ctx)
	c.DB.Close(context.Background())
}

// Execute performs a raw query against SurrealDB
func (c *Client) Execute(sql string) (interface{}, error) {
	// Query exists in v1.0.0 as package function
	res, err := surrealdb.Query[interface{}](context.Background(), c.DB, sql, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	// Unwrap if single result for backward compatibility
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}

// SmartQuery is a helper for parameterized queries
func (c *Client) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	varsMap, ok := vars.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("vars must be map[string]interface{}")
	}
	res, err := surrealdb.Query[interface{}](context.Background(), c.DB, sql, varsMap)
	if err != nil {
		return nil, err
	}
	// Unwrap if single result
	if len(*res) == 1 {
		return (*res)[0].Result, nil
	}
	return res, nil
}
