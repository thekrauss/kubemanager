package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

//go:generate mockgen -source=publisher.go -destination=mocks/mock_Publisher.go -package=mocks
type Publisher interface {
	Publish(ctx context.Context, channel string, data any) error
}

type httpPublisher struct {
	enabled bool
	apiURL  string
	apiKey  string
	client  *http.Client
}

func NewPublisher(cfg configs.CentrifugoConfig) (Publisher, error) {
	if !cfg.Enabled {
		return noopPublisher{}, nil
	}
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiURL == "" {
		return nil, fmt.Errorf("centrifugo api_url is required when realtime is enabled")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("centrifugo api_key is required when realtime is enabled")
	}
	return &httpPublisher{
		enabled: true,
		apiURL:  apiURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}, nil
}

func (p *httpPublisher) Publish(ctx context.Context, channel string, data any) error {
	if p == nil || !p.enabled {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"channel": strings.TrimSpace(channel),
		"data":    data,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/publish", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("centrifugo publish failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
}

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, string, any) error {
	return nil
}

func ProjectChannel(projectID string) string {
	return fmt.Sprintf("project:%s:events", strings.TrimSpace(projectID))
}

func WorkloadChannel(projectID string, workloadID string) string {
	return fmt.Sprintf("project:%s:workload:%s", strings.TrimSpace(projectID), strings.TrimSpace(workloadID))
}

func DeploymentChannel(projectID string, workloadID string) string {
	return fmt.Sprintf("project:%s:workload:%s:deploy", strings.TrimSpace(projectID), strings.TrimSpace(workloadID))
}
