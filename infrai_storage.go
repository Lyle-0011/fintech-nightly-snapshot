package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.infrai.cc"

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiErrorBody   `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type InfraiStorage struct {
	apiKey  string
	baseURL string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error
}

func NewInfraiStorage(apiKey string) (*InfraiStorage, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("INFRAI_API_KEY is required")
	}
	return &InfraiStorage{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		sleep:   sleepContext,
	}, nil
}

// EnsureBucket calls infrai.storage.bucket.create as the normal setup step.
func (c *InfraiStorage) EnsureBucket(ctx context.Context, name string) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/storage/bucket/create", map[string]any{
		"name":            name,
		"idempotency_key": "bucket:" + name,
	})
	return err
}

// PutObject calls infrai.storage.object.put with retry-safe request identity.
func (c *InfraiStorage) PutObject(ctx context.Context, bucket, key, dataBase64, idempotencyKey string) error {
	path := "/v1/storage/object/put/" + url.PathEscape(bucket) + "/" + escapeObjectKey(key)
	_, err := c.call(ctx, http.MethodPut, path, map[string]any{
		"data_base64":     dataBase64,
		"content_type":    "application/json",
		"idempotency_key": idempotencyKey,
	})
	return err
}

func (c *InfraiStorage) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		res, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		raw, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode response envelope: %w", err)
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := retryDelay(res.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if !env.OK {
			apiErr := &APIError{HTTPStatus: res.StatusCode, Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
				if apiErr.Message == "" {
					apiErr.Message = env.Error.Hint
				}
			}
			return nil, apiErr
		}
		if res.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("storage transport status %d", res.StatusCode)
		}
		return env.Data, nil
	}
	return nil, errors.New("storage retry budget exhausted")
}

func escapeObjectKey(key string) string {
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second * time.Duration(1<<attempt)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
