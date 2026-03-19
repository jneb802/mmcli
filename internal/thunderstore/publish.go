package thunderstore

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	initiateUploadURL = baseURL + "/api/experimental/usermedia/initiate-upload/"
	finishUploadURL   = baseURL + "/api/experimental/usermedia/%s/finish-upload/"
	submitURL         = baseURL + "/api/experimental/submission/submit/"
)

// initiateResp is the response from the upload initiation endpoint.
type initiateResp struct {
	UserMedia struct {
		UUID string `json:"uuid"`
	} `json:"user_media"`
	UploadURLs []uploadPart `json:"upload_urls"`
}

type uploadPart struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

type completedPart struct {
	ETag       string `json:"ETag"`
	PartNumber int    `json:"PartNumber"`
}

type finishReq struct {
	Parts []completedPart `json:"parts"`
}

type submitReq struct {
	AuthorName    string   `json:"author_name"`
	Categories    []string `json:"categories"`
	Communities   []string `json:"communities"`
	HasNSFW       bool     `json:"has_nsfw_content"`
	UploadUUID    string   `json:"upload_uuid"`
}

// BuildModpackZip creates an in-memory zip of the modpack directory.
func BuildModpackZip(modpackPath string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	required := []string{"manifest.json", "README.md", "icon.png"}
	for _, name := range required {
		path := filepath.Join(modpackPath, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("missing required file %s: %w", name, err)
		}
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}

	// Include config/ directory if it exists
	configDir := filepath.Join(modpackPath, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(configDir, e.Name()))
			if err != nil {
				continue
			}
			w, err := zw.Create(filepath.Join("config", e.Name()))
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(data); err != nil {
				return nil, err
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Publish uploads a modpack zip to Thunderstore and submits it.
func Publish(token, authorName, modpackPath string) error {
	zipData, err := BuildModpackZip(modpackPath)
	if err != nil {
		return fmt.Errorf("failed to build zip: %w", err)
	}

	// Step 1: Initiate upload
	initBody, _ := json.Marshal(map[string]interface{}{
		"filename":        "modpack.zip",
		"file_size_bytes": len(zipData),
	})

	initResp, err := doAuthJSON(token, "POST", initiateUploadURL, initBody)
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %w", err)
	}

	var initData initiateResp
	if err := json.Unmarshal(initResp, &initData); err != nil {
		return fmt.Errorf("failed to parse upload initiation: %w", err)
	}

	uuid := initData.UserMedia.UUID
	if uuid == "" {
		return fmt.Errorf("upload initiation returned no UUID")
	}

	// Step 2: Upload chunks
	var completed []completedPart
	for _, part := range initData.UploadURLs {
		end := part.Offset + part.Length
		if end > int64(len(zipData)) {
			end = int64(len(zipData))
		}
		chunk := zipData[part.Offset:end]

		hash := md5.Sum(chunk)
		md5Hex := hex.EncodeToString(hash[:])

		req, err := http.NewRequest("PUT", part.URL, bytes.NewReader(chunk))
		if err != nil {
			return fmt.Errorf("failed to create chunk request: %w", err)
		}
		req.Header.Set("Content-MD5", md5Hex)
		req.ContentLength = int64(len(chunk))
		req.Header.Set("Connection", "keep-alive")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to upload chunk %d: %w", part.PartNumber, err)
		}
		resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("chunk upload %d failed: HTTP %d", part.PartNumber, resp.StatusCode)
		}

		etag := resp.Header.Get("ETag")
		completed = append(completed, completedPart{
			ETag:       etag,
			PartNumber: part.PartNumber,
		})
	}

	// Step 3: Finish upload
	finishBody, _ := json.Marshal(finishReq{Parts: completed})
	finishURL := fmt.Sprintf(finishUploadURL, uuid)
	if _, err := doAuthJSON(token, "POST", finishURL, finishBody); err != nil {
		return fmt.Errorf("failed to finish upload: %w", err)
	}

	// Step 4: Submit package
	submitBody, _ := json.Marshal(submitReq{
		AuthorName:  authorName,
		Categories:  []string{},
		Communities: []string{"valheim"},
		HasNSFW:     false,
		UploadUUID:  uuid,
	})
	if _, err := doAuthJSON(token, "POST", submitURL, submitBody); err != nil {
		return fmt.Errorf("failed to submit package: %w", err)
	}

	return nil
}

func doAuthJSON(token, method, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
