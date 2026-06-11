package gitrepo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/schema"
)

type CredentialStore struct {
	db db.Executor
}

func NewCredentialStore(db db.Executor) *CredentialStore {
	return &CredentialStore{db: db}
}

func (s *CredentialStore) SaveCredential(ctx context.Context, cred Credential) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("database client not initialized")
	}

	id := fmt.Sprintf("%s:%s", schema.TableGitCredential, db.SanitizeID(cred.Target))
	query := fmt.Sprintf("UPSERT %s CONTENT $cred;", id)
	vars := map[string]interface{}{
		"cred": cred,
	}
	_, err := s.db.SmartQuery(ctx, query, vars)
	return err
}

func (s *CredentialStore) GetCredential(ctx context.Context, target string) (*Credential, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	id := fmt.Sprintf("%s:%s", schema.TableGitCredential, db.SanitizeID(target))
	query := fmt.Sprintf("SELECT * FROM %s;", id)
	res, err := s.db.Execute(ctx, query)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	var rawData interface{}
	switch val := res.(type) {
	case []interface{}:
		if len(val) == 0 {
			return nil, nil
		}
		rawData = val[0]
	default:
		rawData = val
	}

	m, ok := rawData.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	var cred Credential
	if err := json.Unmarshal(b, &cred); err != nil {
		return nil, err
	}

	return &cred, nil
}
