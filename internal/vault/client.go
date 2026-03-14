package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/edgeflux/edgeflux/internal/pki"
)

// Client stores approved device certificate records in Vault KV v2.
// It targets the default dev mount: secret/
type Client struct {
	addr   string
	token  string
	http   *http.Client
	mount  string
	prefix string
}

func NewClient(addr, token string) *Client {
	return &Client{
		addr:   strings.TrimRight(addr, "/"),
		token:  token,
		http:   &http.Client{Timeout: 5 * time.Second},
		mount:  "secret",
		prefix: "edgeflux",
	}
}

func (c *Client) Health() error {
	req, err := http.NewRequest(http.MethodGet, c.addr+"/v1/sys/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vault health check failed: %d %s", resp.StatusCode, string(body))
}

func (c *Client) StoreApprovedCert(deviceID string, bundle *pki.CertBundle) error {
	if bundle == nil || bundle.Cert == nil {
		return fmt.Errorf("nil cert bundle")
	}
	data := map[string]any{
		"device_id":       deviceID,
		"status":          "approved",
		"cert_serial":     bundle.Serial,
		"cert_thumbprint": bundle.Thumbprint,
		"cert_pem":        string(bundle.CertPEM),
		"issued_at":       bundle.Cert.NotBefore.Format(time.RFC3339),
		"not_after":       bundle.Cert.NotAfter.Format(time.RFC3339),
		"updated_at":      time.Now().UTC().Format(time.RFC3339),
	}
	return c.writeKV(c.path("devices", deviceID), data)
}

func (c *Client) MarkRevoked(deviceID, serial, reason string, revokedAt time.Time) error {
	if reason == "" {
		reason = "manual operator action"
	}
	data := map[string]any{
		"device_id":       deviceID,
		"status":          "revoked",
		"cert_serial":     serial,
		"reason":          reason,
		"revoked_at":      revokedAt.UTC().Format(time.RFC3339),
		"recorded_at":     time.Now().UTC().Format(time.RFC3339),
		"managed_by":      "edgeflux-server",
		"requires_action": "reenroll",
	}
	return c.writeKV(c.path("revocations", deviceID), data)
}

func (c *Client) GetApprovedCertRecord(deviceID string) (map[string]any, error) {
	return c.readKV(c.path("devices", deviceID))
}

func (c *Client) path(kind, deviceID string) string {
	return fmt.Sprintf("%s/data/%s/%s/%s", c.mount, c.prefix, kind, url.PathEscape(deviceID))
}

func (c *Client) writeKV(path string, data map[string]any) error {
	payload, _ := json.Marshal(map[string]any{"data": data})
	req, err := http.NewRequest(http.MethodPost, c.addr+"/v1/"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vault write failed: %d %s", resp.StatusCode, string(body))
}

func (c *Client) readKV(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, c.addr+"/v1/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vault read failed: %d %s", resp.StatusCode, string(body))
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	data, _ := out["data"].(map[string]any)
	record, _ := data["data"].(map[string]any)
	if record == nil {
		return nil, fmt.Errorf("invalid vault record")
	}
	return record, nil
}
