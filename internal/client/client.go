package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"mmcli/internal/agentapi"
)

type AgentClient struct {
	BaseURL string
	Secret  string
	HTTP    *http.Client
}

func New(host string, port int, secret string) *AgentClient {
	return &AgentClient{
		BaseURL: fmt.Sprintf("http://%s:%d", host, port),
		Secret:  secret,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *AgentClient) Status() (*agentapi.StatusResponse, error) {
	var resp agentapi.StatusResponse
	if err := c.doJSON("GET", agentapi.PathStatus, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) Start() (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathStart, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) Stop() (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathStop, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) Restart() (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathRestart, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) ListMods() (*agentapi.ModListResponse, error) {
	var resp agentapi.ModListResponse
	if err := c.doJSON("GET", agentapi.PathMods, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) PushMods(archive io.Reader, clean bool) (*agentapi.ActionResponse, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if clean {
		w.WriteField("clean", "true")
	} else {
		w.WriteField("clean", "false")
	}

	part, err := w.CreateFormFile("archive", "mods.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, archive); err != nil {
		return nil, fmt.Errorf("failed to write archive: %w", err)
	}
	w.Close()

	req, err := http.NewRequest("POST", c.BaseURL+agentapi.PathMods, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)
	req.Header.Set("Content-Type", w.FormDataContentType())

	// Use a longer timeout for uploads
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("push failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var errResp agentapi.ErrorResponse
		json.NewDecoder(httpResp.Body).Decode(&errResp)
		return nil, fmt.Errorf("push failed (%d): %s", httpResp.StatusCode, errResp.Error)
	}

	var resp agentapi.ActionResponse
	json.NewDecoder(httpResp.Body).Decode(&resp)
	return &resp, nil
}

func (c *AgentClient) Logs(lines int, follow bool) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s%s?lines=%d&follow=%v", c.BaseURL, agentapi.PathLogs, lines, follow)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)

	// No timeout for streaming
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("logs failed (%d): %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

func (c *AgentClient) doJSON(method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not connect to server (is the agent running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp agentapi.ErrorResponse
		json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}
