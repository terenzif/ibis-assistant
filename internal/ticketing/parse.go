package ticketing

import (
	"encoding/json"
	"fmt"
)

func ParseProviderFieldsJSON(raw string) (map[string]interface{}, error) {
	if raw == "" {
		return map[string]interface{}{}, nil
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid provider_fields_json: %w", err)
	}
	return out, nil
}
