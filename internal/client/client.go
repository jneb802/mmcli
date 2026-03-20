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

func (c *AgentClient) ListPlayers() (*agentapi.PlayersResponse, error) {
	var resp agentapi.PlayersResponse
	if err := c.doJSON("GET", agentapi.PathPlayers, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) GetWebhookConfig() (*agentapi.WebhookConfigResponse, error) {
	var resp agentapi.WebhookConfigResponse
	if err := c.doJSON("GET", agentapi.PathWebhook, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) UpdateWebhookConfig(req agentapi.WebhookConfigUpdate) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathWebhook, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SyncMods sends a manifest and optional upload mod zips to the server.
// The uploads map is keyed by DirName → zip data for mods with Source="upload".
func (c *AgentClient) SyncMods(manifest agentapi.PushManifest, uploads map[string]io.Reader) (*agentapi.SyncResponse, error) {
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()

		// Write manifest as JSON form field
		data, err := json.Marshal(manifest)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if err := w.WriteField("manifest", string(data)); err != nil {
			pw.CloseWithError(err)
			return
		}

		// Write upload mod zips as file parts
		for dirName, r := range uploads {
			part, err := w.CreateFormFile(dirName, dirName+".zip")
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(part, r); err != nil {
				pw.CloseWithError(err)
				return
			}
		}

		w.Close()
	}()

	req, err := http.NewRequest("POST", c.BaseURL+agentapi.PathModsSync, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)
	req.Header.Set("Content-Type", w.FormDataContentType())

	// No timeout — server may need to download from Thunderstore + receive uploads
	httpClient := &http.Client{}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var errResp agentapi.ErrorResponse
		json.NewDecoder(httpResp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return nil, fmt.Errorf("sync failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("sync failed with status %d", httpResp.StatusCode)
	}

	var resp agentapi.SyncResponse
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

func (c *AgentClient) GetSettings() (*agentapi.SettingsResponse, error) {
	var resp agentapi.SettingsResponse
	if err := c.doJSON("GET", agentapi.PathSettings, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) UpdateSettings(req *agentapi.SettingsUpdateRequest) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathSettings, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) ListWorlds() (*agentapi.WorldListResponse, error) {
	var resp agentapi.WorldListResponse
	if err := c.doJSON("GET", agentapi.PathWorlds, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) DeleteWorld(name string) (*agentapi.WorldDeleteResponse, error) {
	var resp agentapi.WorldDeleteResponse
	if err := c.doJSON("POST", agentapi.PathWorldDelete, agentapi.WorldDeleteRequest{Name: name}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) UploadWorld(name string, dbData, fwlData io.Reader) (*agentapi.WorldUploadResponse, error) {
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		if err := w.WriteField("name", name); err != nil {
			pw.CloseWithError(err)
			return
		}
		dbPart, err := w.CreateFormFile("db", name+".db")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(dbPart, dbData); err != nil {
			pw.CloseWithError(err)
			return
		}
		fwlPart, err := w.CreateFormFile("fwl", name+".fwl")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(fwlPart, fwlData); err != nil {
			pw.CloseWithError(err)
			return
		}
		w.Close()
	}()

	req, err := http.NewRequest("POST", c.BaseURL+agentapi.PathWorldUpload, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentapi.HeaderAPIKey, c.Secret)
	req.Header.Set("Content-Type", w.FormDataContentType())

	httpClient := &http.Client{}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("world upload failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var errResp agentapi.ErrorResponse
		json.NewDecoder(httpResp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return nil, fmt.Errorf("world upload failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("world upload failed with status %d", httpResp.StatusCode)
	}

	var resp agentapi.WorldUploadResponse
	json.NewDecoder(httpResp.Body).Decode(&resp)
	return &resp, nil
}

func (c *AgentClient) ListLaunchConfigs() (*agentapi.LaunchConfigListResponse, error) {
	var resp agentapi.LaunchConfigListResponse
	if err := c.doJSON("GET", agentapi.PathLaunchConfigs, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) CreateLaunchConfig(req agentapi.LaunchConfigCreateRequest) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathLaunchConfigs, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) GetLaunchConfig(name string) (*agentapi.LaunchConfig, error) {
	var resp agentapi.LaunchConfig
	if err := c.doJSON("GET", agentapi.PathLaunchConfigs+"/"+name, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) UpdateLaunchConfig(name string, settings *agentapi.SettingsResponse) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("PUT", agentapi.PathLaunchConfigs+"/"+name, settings, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) DeleteLaunchConfig(name string) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("DELETE", agentapi.PathLaunchConfigs+"/"+name, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) ActivateLaunchConfig(name string) (*agentapi.ActionResponse, error) {
	var resp agentapi.ActionResponse
	if err := c.doJSON("POST", agentapi.PathLaunchConfigsActive, agentapi.LaunchConfigActivateRequest{Name: name}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) Update() (*agentapi.UpdateResponse, error) {
	var resp agentapi.UpdateResponse
	if err := c.doJSON("POST", agentapi.PathUpdate, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AgentClient) doJSON(method, path string, body any, result any) error {
	return c.doJSONWithClient(c.HTTP, method, path, body, result)
}

func (c *AgentClient) doJSONWithTimeout(method, path string, body any, result any, timeout time.Duration) error {
	return c.doJSONWithClient(&http.Client{Timeout: timeout}, method, path, body, result)
}

func (c *AgentClient) doJSONWithClient(httpClient *http.Client, method, path string, body any, result any) error {
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

	resp, err := httpClient.Do(req)
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
