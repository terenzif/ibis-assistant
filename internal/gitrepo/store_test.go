package gitrepo

import (
	"context"
	"testing"
)


type MockDB struct {
	SaveFunc func(ctx context.Context, sql string, vars interface{}) (interface{}, error)
	ExecFunc func(ctx context.Context, sql string) (interface{}, error)
}

func (m *MockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, sql)
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	if m.SaveFunc != nil {
		return m.SaveFunc(ctx, sql, vars)
	}
	return nil, nil
}

func (m *MockDB) Close() {}

func TestCredentialStore_SaveAndGet(t *testing.T) {
	var savedVars interface{}
	var executedSQL string

	mock := &MockDB{
		SaveFunc: func(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
			savedVars = vars
			return nil, nil
		},
		ExecFunc: func(ctx context.Context, sql string) (interface{}, error) {
			executedSQL = sql
			// Return a single record matching the select query
			return []interface{}{
				map[string]interface{}{
					"target":    "github.com",
					"provider":  "github",
					"auth_type": "token",
					"token":     "ghp_test123",
				},
			}, nil
		},
	}

	store := NewCredentialStore(mock)

	// Test Save
	cred := Credential{
		Target:   "github.com",
		Provider: "github",
		AuthType: AuthTypeToken,
		Token:    "ghp_test123",
	}

	err := store.SaveCredential(context.Background(), cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	varsMap, ok := savedVars.(map[string]interface{})
	if !ok {
		t.Fatalf("saved vars should be map[string]interface{}")
	}

	savedCred := varsMap["cred"].(Credential)
	if savedCred.Token != "ghp_test123" {
		t.Errorf("expected token ghp_test123, got %s", savedCred.Token)
	}

	// Test Get
	retrieved, err := store.GetCredential(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved == nil {
		t.Fatalf("expected to retrieve a credential, got nil")
	}

	if retrieved.Token != "ghp_test123" {
		t.Errorf("expected token ghp_test123, got %s", retrieved.Token)
	}

	expectedSQL := "SELECT * FROM git_credential:github_com;"
	if executedSQL != expectedSQL {
		t.Errorf("expected SQL %q, got %q", expectedSQL, executedSQL)
	}
}

func TestCredentialStore_GetNotFound(t *testing.T) {
	mock := &MockDB{
		ExecFunc: func(ctx context.Context, sql string) (interface{}, error) {
			// Return empty array (not found)
			return []interface{}{}, nil
		},
	}

	store := NewCredentialStore(mock)
	retrieved, err := store.GetCredential(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Errorf("expected nil result, got %+v", retrieved)
	}
}

func TestCredentialStore_NilDB(t *testing.T) {
	var store *CredentialStore = nil
	err := store.SaveCredential(context.Background(), Credential{})
	if err == nil {
		t.Error("expected error on nil store Save, got nil")
	}

	res, err := store.GetCredential(context.Background(), "github.com")
	if err != nil {
		t.Errorf("unexpected error on nil store Get: %v", err)
	}
	if res != nil {
		t.Errorf("expected nil result on nil store Get, got %+v", res)
	}
}
