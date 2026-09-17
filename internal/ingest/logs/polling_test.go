package logs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/terenzif/ibis-server/internal/config"
)

type MockPollingDB struct {
	Queries []string
}

func (m *MockPollingDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.Queries = append(m.Queries, sql)
	if strings.Contains(sql, "SELECT") {
		return []interface{}{}, nil
	}
	return nil, nil
}

func (m *MockPollingDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

func (m *MockPollingDB) Close() {}

func TestPollingManagerLifecycle(t *testing.T) {
	cfg := &config.Config{
		LogsRoot: "test_logs",
		LogIngestion: config.LogIngestionConfig{
			Polling: []config.PollingSource{
				{
					Name:         "test-ftp",
					Enabled:      true,
					Protocol:     "ftp",
					Host:         "127.0.0.1",
					Port:         9999, // Unreachable port
					User:         "user",
					Password:     "pass",
					RemoteDir:    "logs",
					FilePattern:  "*.log",
					PollInterval: "10ms",
				},
				{
					Name:         "disabled-source",
					Enabled:      false,
					Protocol:     "sftp",
					Host:         "127.0.0.1",
					Port:         22,
					PollInterval: "1h",
				},
			},
		},
	}

	dbMock := &MockPollingDB{}
	pm := NewPollingManager(cfg, dbMock, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	pm.Start(ctx)

	// Allow some ticks/attemps to run
	time.Sleep(50 * time.Millisecond)

	// Stop the manager (verifies clean shutdown and no deadlock)
	pm.Stop()
}
