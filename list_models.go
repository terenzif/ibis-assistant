package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func main() {
	key := ""
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + key
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %s\n", resp.Status)
	
	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if models, ok := data["models"].([]interface{}); ok {
		for _, m := range models {
			model := m.(map[string]interface{})
			fmt.Printf("- %v\n", model["name"])
		}
	} else {
		fmt.Printf("Body: %s\n", string(body))
	}
}
