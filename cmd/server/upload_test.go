package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/terenzif/ibis-assistant/internal/config"
)

type MockUploadDB struct {
	Captured []string
}

func (m *MockUploadDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.Captured = append(m.Captured, sql)
	if strings.Contains(sql, "SELECT") {
		return []interface{}{}, nil
	}
	return nil, nil
}

func (m *MockUploadDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

func (m *MockUploadDB) Close() {}

func TestLogUploadHandler(t *testing.T) {
	cfg := &config.Config{
		LogsRoot: "test_logs",
		LogIngestion: config.LogIngestionConfig{
			HTTP: config.HTTPIngestionConfig{
				Enabled: true,
				APIKey:  "secret-key",
			},
		},
	}

	dbMock := &MockUploadDB{}

	handler := logUploadHandler(cfg, dbMock, nil)

	// Case 1: Unauthorized (missing API key)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/upload", bytes.NewBufferString("log line"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	// Case 2: Method not allowed (GET)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/logs/upload", nil)
	req.Header.Set("X-API-Key", "secret-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}

	// Case 3: JSON body parse and handle with AI disabled / none functional
	uploadReq := map[string]interface{}{
		"project":   "project-x",
		"file_name": "upload.log",
		"lines":     []string{"error line 1", "error line 2"},
	}
	bodyBytes, _ := json.Marshal(uploadReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/logs/upload", bytes.NewReader(bodyBytes))
	req.Header.Set("X-API-Key", "secret-key")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AI client is not functional") {
		t.Errorf("Expected body to contain 'AI client is not functional', got %q", rec.Body.String())
	}

	// Case 4: Ingestion Disabled
	cfg.LogIngestion.HTTP.Enabled = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/logs/upload", bytes.NewBufferString("log line"))
	req.Header.Set("X-API-Key", "secret-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}
