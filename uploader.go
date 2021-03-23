package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type fileUploader struct {
	client *http.Client
}

func newFileUploader(client *http.Client) *fileUploader {
	return &fileUploader{
		client: client,
	}
}

func uploadURLErr(err error) error {
	return fmt.Errorf("failed to request upload URL: %w", err)
}

func (u *fileUploader) requestUploadURL(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", uploadURLErr(err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return "", uploadURLErr(err)
	}
	defer resp.Body.Close()
	var respData struct {
		URL, Error string
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		io.Copy(io.Discard, resp.Body)
		return "", uploadURLErr(err)
	}
	if resp.StatusCode != 200 {
		return "", uploadURLErr(errors.New(respData.Error))
	}
	return respData.URL, nil
}

func uploadFileErr(err error) error {
	return fmt.Errorf("failed to upload file: %w", err)
}

func (u *fileUploader) uploadFile(ctx context.Context, url string, file io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", url, file)
	if err != nil {
		return uploadFileErr(err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return uploadFileErr(err)
	}
	defer resp.Body.Close()
	msg, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadFileErr(err)
	}
	if resp.StatusCode != 200 {
		return uploadFileErr(fmt.Errorf("HTTP error: code %d, %s", resp.StatusCode, msg))
	}
	return nil
}
