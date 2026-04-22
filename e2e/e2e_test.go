//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// E2E tests for the pura CLI against a running API server.
// Run with: go test -tags=e2e ./e2e/... -v
// Requires: PURA_E2E_URL environment variable (default: http://localhost:8787)

func apiURL() string {
	if u := os.Getenv("PURA_E2E_URL"); u != "" {
		return u
	}
	return "http://localhost:8787"
}

// isServerUp checks that apiURL() exposes a Pura-shaped /health response.
// We match on the JSON signature rather than just HTTP 200 so a stray
// service on the same port won't pretend to be Pura and wedge the suite.
func isServerUp() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(apiURL() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var body struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.OK && body.Service == "pura"
}

func puraCmd(args ...string) (string, error) {
	// Build path: assume binary is at cli/pura
	binary := "./pura"
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		binary = "../pura"
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "PURA_API_URL="+apiURL())
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func TestE2EFullCycle(t *testing.T) {
	if !isServerUp() {
		t.Skip("API server not running at " + apiURL())
	}

	// Step 1: Create via API (curl-style, not CLI, since CLI needs stdin/file)
	body := `{"content":"# CLI E2E Test\n\nCreated for e2e.","substrate":"markdown","title":"CLI E2E"}`
	resp, err := http.Post(apiURL()+"/api/p", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var createResp struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug  string `json:"slug"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	slug := createResp.Data.Slug
	token := createResp.Data.Token
	t.Logf("Created: slug=%s token=%s", slug, token)

	// Step 2: Get via CLI
	out, err := puraCmd("get", slug, "--json", "--api-url", apiURL())
	if err != nil {
		t.Fatalf("pura get failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, slug) {
		t.Errorf("get output should contain slug %q, got: %s", slug, out)
	}

	// Step 3: Get raw
	out, err = puraCmd("get", slug, "-f", "raw", "--api-url", apiURL())
	if err != nil {
		t.Fatalf("pura get raw failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "# CLI E2E Test") {
		t.Errorf("raw should contain markdown, got: %s", out)
	}

	// Step 4: List (with token)
	out, err = puraCmd("ls", "--json", "--token", token, "--api-url", apiURL())
	if err != nil {
		t.Fatalf("pura ls failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, slug) {
		t.Errorf("ls should contain slug %q, got: %s", slug, out)
	}

	// Step 5: Delete via API
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/api/p/%s", apiURL(), slug), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", delResp.StatusCode)
	}

	// Step 6: Confirm 404
	getResp, _ := http.Get(fmt.Sprintf("%s/api/p/%s", apiURL(), slug))
	if getResp != nil {
		getResp.Body.Close()
		if getResp.StatusCode != 404 {
			t.Errorf("expected 404 after delete, got %d", getResp.StatusCode)
		}
	}
}
