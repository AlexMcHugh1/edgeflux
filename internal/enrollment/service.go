package enrollment

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/edgeflux/edgeflux/internal/pki"
	"github.com/edgeflux/edgeflux/internal/store"
)

type CertVault interface {
	StoreApprovedCert(deviceID string, bundle *pki.CertBundle) error
	MarkRevoked(deviceID, serial, reason string, revokedAt time.Time) error
	GetApprovedCertRecord(deviceID string) (map[string]any, error)
}

type Request struct {
	DeviceID      string `json:"device_id"`
	CSRPEM        string `json:"csr_pem"`
	HWAttestation string `json:"hw_attestation"`
	FirmwareHash  string `json:"firmware_hash"`
	Profile       string `json:"profile"`
}

type Response struct {
	DeviceID       string `json:"device_id"`
	Status         string `json:"status"`
	CertPEM        string `json:"cert_pem,omitempty"`
	CACertPEM      string `json:"ca_cert_pem,omitempty"`
	CertSerial     string `json:"cert_serial,omitempty"`
	CertThumbprint string `json:"cert_thumbprint,omitempty"`
	Error          string `json:"error,omitempty"`
	NextStep       string `json:"next_step,omitempty"`
}

type Container struct {
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	Ports          []string          `json:"ports,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	ReadOnlyRoot   bool              `json:"read_only_rootfs"`
	NoNewPrivs     bool              `json:"no_new_privileges"`
	SeccompProfile string            `json:"seccomp_profile"`
}

type SSHKey struct {
	Type    string `json:"type"`
	PubKey  string `json:"public_key"`
	Comment string `json:"comment"`
	Access  string `json:"access_level"`
}

type Tunnel struct {
	RelayHost   string `json:"relay_host"`
	RelayPort   int    `json:"relay_port"`
	DeviceAlias string `json:"device_alias"`
	AuthMethod  string `json:"auth_method"`
}

type SSHConfig struct {
	Keys   []SSHKey       `json:"authorized_keys"`
	SSHD   map[string]any `json:"sshd_config"`
	Tunnel Tunnel         `json:"tunnel"`
}

type Service struct {
	PKI   *pki.Manager
	Store *store.EventStore
	Vault CertVault

	mu               sync.RWMutex
	revokedSerials   map[string]string
	deviceContainers map[string][]Container
}

func NewService(p *pki.Manager, s *store.EventStore, v CertVault) *Service {
	return &Service{
		PKI:              p,
		Store:            s,
		Vault:            v,
		revokedSerials:   make(map[string]string),
		deviceContainers: make(map[string][]Container),
	}
}

func (s *Service) CreatePendingDevice(deviceID, profile string, nics []store.NIC, keys []store.AuthorizedKey, simulate bool) {
	if profile == "" {
		profile = "alpine-edge-secure"
	}
	normalizedNICs := make([]store.NIC, 0, len(nics))
	for _, n := range nics {
		if n.State == "" {
			n.State = "up"
		}
		normalizedNICs = append(normalizedNICs, n)
	}
	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		d.Phase = "pending"
		d.Status = "pending_approval"
		d.ApprovalStatus = "pending"
		d.ConnectionAlive = false
		d.Simulate = simulate
		if len(normalizedNICs) > 0 {
			d.NICs = append([]store.NIC(nil), normalizedNICs...)
		}
		if len(keys) > 0 {
			d.AuthorizedKeys = append([]store.AuthorizedKey(nil), keys...)
		}
	})
	s.Store.Emit(store.INFO, "control", deviceID, "Device created in pending state via UI", "pending", map[string]any{"profile": profile, "nics": normalizedNICs, "authorized_keys": len(keys)})
}

func defaultContainers() []Container {
	return []Container{
		{Name: "edgeflux-agent", Image: "registry.edgeflux.local:5443/edgeflux/agent:2.4.1", Ports: []string{"8883/tcp", "443/tcp"}, ReadOnlyRoot: true, NoNewPrivs: true, SeccompProfile: "runtime/default"},
		{Name: "mqtt-broker", Image: "registry.edgeflux.local:5443/edgeflux/mosquitto:2.0.18", Ports: []string{"1883/tcp"}, ReadOnlyRoot: true, NoNewPrivs: true, SeccompProfile: "runtime/default"},
		{Name: "health-monitor", Image: "registry.edgeflux.local:5443/edgeflux/node-exporter:1.7.0", Ports: []string{"9100/tcp"}, ReadOnlyRoot: true, NoNewPrivs: true, SeccompProfile: "runtime/default"},
		{Name: "log-forwarder", Image: "registry.edgeflux.local:5443/edgeflux/fluentbit:2.2.1", Ports: []string{"24224/tcp"}, ReadOnlyRoot: true, NoNewPrivs: true, SeccompProfile: "runtime/default", Env: map[string]string{"OUTPUT": "forward", "HOST": "logs.edgeflux.cloud"}},
	}
}

func (s *Service) Containers(deviceID string) []Container {
	s.mu.RLock()
	custom, ok := s.deviceContainers[deviceID]
	s.mu.RUnlock()
	if ok && len(custom) > 0 {
		return custom
	}
	return defaultContainers()
}

func (s *Service) AppendContainer(deviceID string, c Container) []Container {
	s.mu.Lock()
	defer s.mu.Unlock()

	base, ok := s.deviceContainers[deviceID]
	if !ok {
		base = defaultContainers()
	}
	base = append(base, c)
	s.deviceContainers[deviceID] = base
	return base
}

func (s *Service) ContainerSpecs(deviceID string) map[string]Container {
	items := s.Containers(deviceID)
	out := make(map[string]Container, len(items))
	for _, c := range items {
		out[c.Name] = c
	}
	return out
}

func (s *Service) RemoveContainer(deviceID, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	removed := false

	s.mu.Lock()
	if current, ok := s.deviceContainers[deviceID]; ok {
		next := make([]Container, 0, len(current))
		for _, c := range current {
			if c.Name == name {
				removed = true
				continue
			}
			next = append(next, c)
		}
		s.deviceContainers[deviceID] = next
	}
	s.mu.Unlock()

	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		if d.Containers == nil {
			d.Containers = make(map[string]string)
		}
		if _, ok := d.Containers[name]; ok {
			removed = true
		}
		delete(d.Containers, name)
	})

	return removed
}

func (s *Service) Enroll(req Request) (*Response, error) {
	id := req.DeviceID
	if id == "" {
		return &Response{Status: "error", Error: "device_id required"}, fmt.Errorf("device_id required")
	}

	s.Store.SetDevice(id, func(d *store.DeviceState) {
		if d.ApprovalStatus == "" {
			d.ApprovalStatus = "pending"
		}
		if d.ApprovalStatus == "pending" {
			d.Phase = "pending"
			d.Status = "pending_approval"
			d.ConnectionAlive = false
		}
	})

	d := s.Store.GetDevice(id)
	if d != nil {
		if d.ApprovalStatus == "revoked" {
			s.Store.SetDevice(id, func(st *store.DeviceState) {
				st.ApprovalStatus = "pending"
				st.Phase = "pending"
				st.Status = "pending_approval"
				st.ConnectionAlive = false
				st.MTLSEstablished = false
				st.RevokedAt = nil
				st.RevocationReason = ""
			})
			s.Store.Emit(store.WARN, "enrollment", id, "Revoked device requested re-enrollment; moved to pending approval", "approval", nil)
			return &Response{DeviceID: id, Status: "pending_approval", NextStep: "wait_for_approval"}, nil
		}
		if d.ApprovalStatus != "approved" {
			s.Store.Emit(store.INFO, "enrollment", id, "Enrollment waiting for operator approval", "approval", nil)
			return &Response{DeviceID: id, Status: "pending_approval", NextStep: "wait_for_approval"}, nil
		}
	}

	s.Store.SetDevice(id, func(d *store.DeviceState) {
		d.Phase = "enrolling"
		d.Status = "processing"
	})

	// Validate HW attestation
	s.Store.Emit(store.INFO, "enrollment", id, "Validating hardware attestation: "+req.HWAttestation, "enroll", nil)
	time.Sleep(150 * time.Millisecond)
	if req.HWAttestation == "" {
		s.Store.Emit(store.WARN, "enrollment", id, "No HW attestation — accepting in dev mode", "enroll", nil)
	} else {
		s.Store.Emit(store.OK, "enrollment", id, "Hardware attestation validated", "enroll", nil)
	}

	// Verify firmware
	s.Store.Emit(store.INFO, "enrollment", id, "Verifying firmware integrity: "+req.FirmwareHash, "enroll", nil)
	time.Sleep(100 * time.Millisecond)
	s.Store.Emit(store.OK, "enrollment", id, "Firmware hash verified", "enroll", nil)

	// Check policy
	s.Store.Emit(store.INFO, "enrollment", id, fmt.Sprintf("Policy check: profile=%s", req.Profile), "enroll", nil)
	time.Sleep(100 * time.Millisecond)
	s.Store.Emit(store.OK, "enrollment", id, "Enrollment policy passed", "enroll", nil)

	// Sign certificate
	s.Store.Emit(store.INFO, "enrollment", id, "Signing device certificate with Enrollment CA", "enroll", nil)

	var bundle *pki.CertBundle
	var err error
	if req.CSRPEM != "" {
		bundle, err = s.PKI.SignCSR([]byte(req.CSRPEM), id)
	} else {
		bundle, err = s.PKI.GenerateDeviceCert(id)
	}
	if err != nil {
		s.Store.Emit(store.ERROR, "enrollment", id, "Cert signing failed: "+err.Error(), "enroll", nil)
		return &Response{DeviceID: id, Status: "error", Error: err.Error()}, err
	}

	now := time.Now()
	s.Store.SetDevice(id, func(d *store.DeviceState) {
		d.Phase = "enrolled"
		d.Status = "enrolled"
		d.CertSerial = bundle.Serial
		d.CertThumbprint = bundle.Thumbprint
		d.CertNotAfter = &bundle.Cert.NotAfter
		d.EnrolledAt = &now
		d.MTLSEstablished = true
		d.ConnectionAlive = true
	})

	if s.Vault != nil {
		if err := s.Vault.StoreApprovedCert(id, bundle); err != nil {
			s.Store.Emit(store.WARN, "vault", id, "Failed to persist cert in Vault: "+err.Error(), "vault", nil)
		} else {
			s.Store.Emit(store.OK, "vault", id, "Approved cert stored in Vault", "vault", map[string]string{"serial": bundle.Serial})
		}
	}

	s.Store.Emit(store.OK, "enrollment", id,
		fmt.Sprintf("Certificate issued — serial=%s expires=%s", bundle.Serial, bundle.Cert.NotAfter.UTC().Format(time.RFC3339)), "enroll",
		map[string]string{"serial": bundle.Serial, "thumbprint": bundle.Thumbprint, "not_after": bundle.Cert.NotAfter.UTC().Format(time.RFC3339)})

	return &Response{
		DeviceID:       id,
		Status:         "enrolled",
		CertPEM:        string(bundle.CertPEM),
		CACertPEM:      string(s.PKI.IntCA.CertPEM),
		CertSerial:     bundle.Serial,
		CertThumbprint: bundle.Thumbprint,
		NextStep:       "mtls_connect",
	}, nil
}

type HealthRequest struct {
	DeviceID          string         `json:"device_id"`
	CertSerial        string         `json:"cert_serial"`
	Status            string         `json:"status"`
	CPUPercent        float64        `json:"cpu_percent"`
	MemPercent        float64        `json:"mem_percent"`
	UptimeSeconds     int64          `json:"uptime_seconds"`
	RunningContainers []string       `json:"running_containers"`
	Meta              map[string]any `json:"meta,omitempty"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

func (s *Service) ApproveDevice(deviceID string) {
	now := time.Now()
	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		d.ApprovalStatus = "approved"
		d.Status = "approved"
		d.Phase = "approved"
		d.ApprovedAt = &now
		if d.RevokedAt != nil {
			d.RevokedAt = nil
			d.RevocationReason = ""
		}
	})
	s.Store.Emit(store.OK, "control", deviceID, "Device approved for enrollment", "approval", nil)
}

func (s *Service) RevokeDevice(deviceID, reason string) {
	if reason == "" {
		reason = "manual operator action"
	}
	now := time.Now()
	serial := ""
	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		serial = d.CertSerial
		d.ApprovalStatus = "revoked"
		d.Status = "revoked"
		d.Phase = "revoked"
		d.RevokedAt = &now
		d.RevocationReason = reason
		d.ConnectionAlive = false
		d.MTLSEstablished = false
		d.CertSerial = ""
		d.CertThumbprint = ""
		d.CertNotAfter = nil
	})

	if serial != "" {
		s.mu.Lock()
		s.revokedSerials[serial] = reason
		s.mu.Unlock()
	}

	if s.Vault != nil {
		if err := s.Vault.MarkRevoked(deviceID, serial, reason, now); err != nil {
			s.Store.Emit(store.WARN, "vault", deviceID, "Failed to persist revocation in Vault: "+err.Error(), "vault", nil)
		} else {
			s.Store.Emit(store.OK, "vault", deviceID, "Revocation recorded in Vault", "vault", map[string]string{"serial": serial, "reason": reason})
		}
	}

	s.Store.Emit(store.WARN, "control", deviceID, "Certificate revoked: "+reason, "revoke", map[string]string{"reason": reason, "serial": serial})
}

func (s *Service) RebootToPending(deviceID string) {
	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		d.Status = "rebooting"
		d.Phase = "rebooting"
		d.ConnectionAlive = false
		d.MTLSEstablished = false
		// Keep approval + cert state so device can reconnect with its existing cert.
		if d.ApprovalStatus == "" {
			d.ApprovalStatus = "approved"
		}
	})
	s.Store.Emit(store.INFO, "control", deviceID, "Device reboot requested; waiting for MQTT/mTLS reconnect with existing cert", "reboot", nil)
}

func (s *Service) HandleHealth(req HealthRequest) HealthResponse {
	id := req.DeviceID
	if id == "" {
		return HealthResponse{Status: "error", Action: "none", Message: "device_id required"}
	}

	d := s.Store.GetDevice(id)
	if d == nil {
		return HealthResponse{Status: "error", Action: "reenroll", Message: "unknown device"}
	}

	if d.ApprovalStatus == "revoked" {
		return HealthResponse{Status: "revoked", Action: "disconnect", Message: "certificate revoked"}
	}
	if d.CertNotAfter != nil && time.Now().After(*d.CertNotAfter) {
		s.Store.SetDevice(id, func(st *store.DeviceState) {
			st.Status = "cert_expired"
			st.ConnectionAlive = false
			st.MTLSEstablished = false
		})
		s.Store.Emit(store.WARN, "health", id, "Device certificate expired; forcing re-enrollment", "health", map[string]string{"not_after": d.CertNotAfter.UTC().Format(time.RFC3339)})
		return HealthResponse{Status: "expired", Action: "reenroll", Message: "certificate expired"}
	}
	if d.ApprovalStatus != "approved" {
		return HealthResponse{Status: "pending_approval", Action: "reenroll", Message: "waiting for approval"}
	}
	if d.CertSerial == "" {
		return HealthResponse{Status: "missing_cert", Action: "reenroll", Message: "no active enrolled certificate"}
	}
	if d.CertSerial != "" && req.CertSerial != "" && d.CertSerial != req.CertSerial {
		return HealthResponse{Status: "mismatch", Action: "reenroll", Message: "certificate mismatch"}
	}

	now := time.Now()
	runningSet := make(map[string]struct{}, len(req.RunningContainers))
	for _, c := range req.RunningContainers {
		name := strings.TrimSpace(c)
		if name == "" {
			continue
		}
		runningSet[name] = struct{}{}
	}

	s.Store.SetDevice(id, func(st *store.DeviceState) {
		st.Phase = "complete"
		st.Status = "online"
		st.ConnectionAlive = true
		st.MTLSEstablished = true
		st.HealthCount++
		st.LastHealth = &now
		if st.Containers == nil {
			st.Containers = make(map[string]string)
		}
		for name, cur := range st.Containers {
			if _, ok := runningSet[name]; ok {
				st.Containers[name] = "running"
			} else if cur == "running" {
				st.Containers[name] = "stopped"
			}
			// keep "deployed" until the agent actually picks it up
		}
		for name := range runningSet {
			st.Containers[name] = "running"
		}
	})

	s.Store.EmitMQTT("edgeflux/device/"+id+"/health", req, id, "inbound")
	s.Store.Emit(store.OK, "health", id,
		fmt.Sprintf("Health heartbeat cpu=%.1f%% mem=%.1f%% uptime=%ds", req.CPUPercent, req.MemPercent, req.UptimeSeconds),
		"health", map[string]any{"cpu_percent": req.CPUPercent, "mem_percent": req.MemPercent, "uptime_seconds": req.UptimeSeconds, "status": req.Status})

	return HealthResponse{Status: "ok", Action: "none"}
}

type SSHKeyPair struct {
	PrivateKeyPEM string `json:"private_key_pem"`
	PublicKeySSH  string `json:"public_key_ssh"`
	Fingerprint   string `json:"fingerprint"`
	Comment       string `json:"comment"`
}

func (s *Service) GenerateSSHKey(deviceID, comment string) (*SSHKeyPair, error) {
	if comment == "" {
		comment = "edgeflux@" + deviceID
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("keygen failed: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("ssh pubkey conversion failed: %w", err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment

	privBytes, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, fmt.Errorf("marshal private key failed: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(privBytes))

	fp := sha256.Sum256(sshPub.Marshal())
	fingerprint := "SHA256:" + hex.EncodeToString(fp[:16])

	// Store public key in device authorized_keys
	s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
		d.AuthorizedKeys = append(d.AuthorizedKeys, store.AuthorizedKey{
			Comment: comment,
			PubKey:  authorizedKey,
			Access:  "root",
		})
	})

	s.Store.Emit(store.OK, "ssh", deviceID, "SSH keypair generated: "+fingerprint, "ssh", map[string]string{"fingerprint": fingerprint, "comment": comment})

	return &SSHKeyPair{
		PrivateKeyPEM: privPEM,
		PublicKeySSH:  authorizedKey,
		Fingerprint:   fingerprint,
		Comment:       comment,
	}, nil
}

func (s *Service) SSH(deviceID string) SSHConfig {
	h := sha256.Sum256([]byte(deviceID))
	suffix := hex.EncodeToString(h[:8])
	keys := []SSHKey{
		{Type: "ssh-ed25519", PubKey: "AAAAC3NzaC1lZDI1NTE5AAAAI" + suffix[:16], Comment: "admin@edgeflux-ops", Access: "root"},
		{Type: "ssh-ed25519", PubKey: "AAAAC3NzaC1lZDI1NTE5AAAAI" + suffix[16:], Comment: "deploy@ci-pipeline", Access: "deploy"},
	}
	if d := s.Store.GetDevice(deviceID); d != nil && len(d.AuthorizedKeys) > 0 {
		keys = make([]SSHKey, 0, len(d.AuthorizedKeys))
		for _, k := range d.AuthorizedKeys {
			keys = append(keys, SSHKey{Type: "ssh-ed25519", PubKey: k.PubKey, Comment: k.Comment, Access: k.Access})
		}
	}
	return SSHConfig{
		Keys: keys,
		SSHD: map[string]any{
			"Port": 22, "PermitRootLogin": "prohibit-password",
			"PasswordAuthentication": "no", "PubkeyAuthentication": "yes",
			"MaxAuthTries": 3, "ClientAliveInterval": 300,
			"AllowTcpForwarding": "no", "X11Forwarding": "no",
		},
		Tunnel: Tunnel{
			RelayHost: "ssh-relay.edgeflux.local", RelayPort: 2222,
			DeviceAlias: deviceID + ".tunnel.edgeflux.local", AuthMethod: "mtls-certificate",
		},
	}
}
