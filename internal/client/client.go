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
	// Stream the multipart body via io.Pipe to avoid buffering large archives in memory
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		w.WriteField("clean", fmt.Sprintf("%v", clean))
		part, err := w.CreateFormFile("archive", "mods.tar.gz")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, archive); err != nil {
			pw.CloseWithError(err)
			return
		}
		w.Close()
	}()

	req, err := http.NewRequest("POST", c.BaseURL+agentapi.PathMods, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)
	req.Header.Set("Content-Type", w.FormDataContentType())

	// No timeout — large uploads can take a long time
	httpClient := &http.Client{}
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

func (c *AgentClient) ListConfigs() (*agentapi.ConfigListResponse, error) {
	var resp agentapi.ConfigListResponse
	if err := c.doJSON("GET", agentapi.PathConfigs, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) GetConfig(filename string) (*agentapi.ConfigFileResponse, error) {
	var resp agentapi.ConfigFileResponse
	if err := c.doJSON("GET", agentapi.PathConfigs+"/"+filename, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) PushConfigs(req agentapi.ConfigPushRequest) (*agentapi.ConfigPushResponse, error) {
	var resp agentapi.ConfigPushResponse
	if err := c.doJSON("POST", agentapi.PathConfigs, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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
