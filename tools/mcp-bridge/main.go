package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	postURL      string
	postURLMutex sync.RWMutex
)

func setPostURL(u string) {
	postURLMutex.Lock()
	defer postURLMutex.Unlock()
	postURL = u
}

func getPostURL() string {
	postURLMutex.RLock()
	defer postURLMutex.RUnlock()
	return postURL
}

func main() {
	sseURL := flag.String("url", "http://localhost:3030/sse", "URL of the SSE endpoint")
	apiKey := flag.String("key", "", "Optional Redmine API Key to forward")
	flag.Parse()

	if *sseURL == "" {
		fmt.Fprintf(os.Stderr, "Error: -url is required\n")
		os.Exit(1)
	}

	// Initial guess for postURL (fallback until 'endpoint' event is received)
	// We guess standard sibling path /message
	defaultPostURL := strings.Replace(*sseURL, "/sse", "/message", 1)
	setPostURL(defaultPostURL)

	fmt.Fprintf(os.Stderr, "MCP Bridge starting...\nConnect SSE: %s\nDefault POST: %s\n", *sseURL, defaultPostURL)

	// 1. Start SSE Listener (Server -> Claude)
	go func() {
		for {
			err := listenSSE(*sseURL, *apiKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "SSE Disconnected: %v. Reconnecting in 1s...\n", err)
				time.Sleep(1 * time.Second)
			}
		}
	}()

	// 2. Start Stdin Listener (Claude -> Server)
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large JSON-RPC messages (e.g. tool results)
	buf := make([]byte, 10*1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	client := &http.Client{Timeout: 30 * time.Second}

	for scanner.Scan() {
		line := scanner.Bytes()

		targetURL := getPostURL()

		// Send JSON-RPC to Server
		req, err := http.NewRequest("POST", targetURL, bytes.NewReader(line))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if *apiKey != "" {
			req.Header.Set("X-Redmine-API-Key", *apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Post failed: %v\n", err)
			continue
		}

		// We expect 200 OK or 202 Accepted.
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "Post error status: %d for URL %s\n", resp.StatusCode, targetURL)
		}
		resp.Body.Close()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Stdin error: %v\n", err)
	}
}

func listenSSE(streamURL, key string) error {
	req, err := http.NewRequest("GET", streamURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if key != "" {
		req.Header.Set("X-Redmine-API-Key", key)
	}

	client := &http.Client{Timeout: 0} // Keep connection open
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("status code %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	// SSE lines can be long? Usually data is one line.
	// We'll use default buffer but maybe increase if needed.

	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		// SSE blocks are separated by empty lines
		if strings.TrimSpace(line) == "" {
			currentEvent = ""
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			if currentEvent == "endpoint" {
				// Server telling us where to post messages (with session ID)
				// Resolve relative URL against streamURL
				baseURL, _ := url.Parse(streamURL)
				refURL, _ := url.Parse(data)
				resolved := baseURL.ResolveReference(refURL)

				newURL := resolved.String()
				fmt.Fprintf(os.Stderr, "Updated POST endpoint: %s\n", newURL)
				setPostURL(newURL)

			} else {
				// 'message' event or implicit message
				// Forward to Stdout (Claude)
				fmt.Println(data)
			}
		}
	}
	return scanner.Err()
}
