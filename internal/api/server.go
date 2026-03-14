package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/edgeflux/edgeflux/internal/enrollment"
	"github.com/edgeflux/edgeflux/internal/pki"
	"github.com/edgeflux/edgeflux/internal/simulator"
	"github.com/edgeflux/edgeflux/internal/store"
	"golang.org/x/crypto/ssh"
	"nhooyr.io/websocket"
)

type Server struct {
	Store           *store.EventStore
	PKI             *pki.Manager
	Enroll          *enrollment.Service
	Simulator       DeviceSimulator
	AutoSimOnCreate bool
	mux             *http.ServeMux
}

type DeviceSimulator interface {
	StartDevice(deviceID, profile string) (string, error)
	InspectDevice(deviceID string) (*simulator.ContainerInfo, error)
	RemoveDevice(deviceID string) error
	ExecCommand(deviceID string) string
	DeployContainer(deviceID, name, image string, ports []string, env map[string]string) (string, error)
	StopContainer(deviceID, name string) error
	DeploySSHProxy(deviceID, publicKey string, sshPort int) (containerID string, hostPort int, err error)
	StopSSHProxy(deviceID string) error
	SSHProxyInfo(deviceID string) (*simulator.SSHProxyInfo, error)
	ListContainerStatuses(deviceID string, containerNames []string) map[string]string
}

func NewServer(es *store.EventStore, p *pki.Manager, e *enrollment.Service) *Server {
	s := &Server{Store: es, PKI: p, Enroll: e, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) SetSimulator(sim DeviceSimulator, autoCreate bool) {
	s.Simulator = sim
	s.AutoSimOnCreate = autoCreate
}

func (s *Server) routes() {
	s.mux.HandleFunc("/events", s.handleSSE)
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })

	s.mux.HandleFunc("/api/v1/stats", s.cors(s.handleStats))
	s.mux.HandleFunc("/api/v1/devices", s.cors(s.handleDevices))
	s.mux.HandleFunc("/api/v1/events", s.cors(s.handleEvents))
	s.mux.HandleFunc("/api/v1/pki/info", s.cors(s.handlePKI))
	s.mux.HandleFunc("/api/v1/enroll", s.cors(s.handleEnroll))
	s.mux.HandleFunc("/api/v1/devices/", s.cors(s.handleDeviceRoutes))

	// Pattern: /api/v1/deploy/{id}/os etc — use path parsing
	s.mux.HandleFunc("/api/v1/deploy/", s.cors(s.handleDeploy))
	s.mux.HandleFunc("/api/v1/config/", s.cors(s.handleConfig))

	// WebSocket SSH terminal (no CORS wrapper — needs raw upgrade)
	s.mux.HandleFunc("/api/v1/ssh-terminal/", s.handleSSHTerminal)

	s.mux.Handle("/", s.staticUIHandler())
}

func (s *Server) staticUIHandler() http.Handler {
	distDir := filepath.Clean("./ui/dist")
	indexPath := filepath.Join(distDir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(indexPath); err != nil {
			http.Error(w, "UI assets are not built. Run `make ui-build` or `cd ui && npm install && npm run build`.", http.StatusServiceUnavailable)
			return
		}

		requestPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if requestPath == "/" {
			http.ServeFile(w, r, indexPath)
			return
		}

		assetPath := filepath.Join(distDir, filepath.FromSlash(strings.TrimPrefix(requestPath, "/")))
		if info, err := os.Stat(assetPath); err == nil && !info.IsDir() {
			http.ServeFile(w, r, assetPath)
			return
		}

		http.ServeFile(w, r, indexPath)
	})
}

func (s *Server) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			return
		}
		next(w, r)
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE unsupported", 500)
		return
	}

	ch := s.Store.Subscribe()
	defer s.Store.Unsubscribe(ch)

	// Send initial state
	init, _ := json.Marshal(map[string]any{"type": "init", "stats": s.Store.Stats(), "devices": s.Store.AllDevices()})
	fmt.Fprintf(w, "event: init\ndata: %s\n\n", init)
	flusher.Flush()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: event\ndata: %s\n\n", evt.JSON())
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-time.After(30 * time.Second):
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var req enrollment.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	s.Store.Emit(store.INFO, "api", req.DeviceID, "Enrollment request received", "enroll", nil)
	s.Store.EmitMQTT("edgeflux/enroll/"+req.DeviceID+"/request", req, req.DeviceID, "inbound")

	resp, err := s.Enroll.Enroll(req)
	if err != nil {
		jsonResp(w, 500, resp)
		return
	}

	if resp.Status == "pending_approval" {
		jsonResp(w, 202, resp)
		return
	}
	if resp.Status == "revoked" {
		jsonResp(w, 403, resp)
		return
	}

	s.Store.EmitMQTT("edgeflux/enroll/"+req.DeviceID+"/response",
		map[string]string{"status": resp.Status, "cert_serial": resp.CertSerial, "next_step": resp.NextStep},
		req.DeviceID, "outbound")

	jsonResp(w, 200, resp)
}

func (s *Server) handleDeviceRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/devices/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		jsonResp(w, 400, map[string]string{"error": "device_id required"})
		return
	}

	deviceID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == "GET" && action == "":
		d := s.Store.GetDevice(deviceID)
		if d == nil {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		jsonResp(w, 200, d)

	case r.Method == "GET" && action == "events":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		jsonResp(w, 200, s.Store.DeviceEvents(deviceID, limit))

	case r.Method == "GET" && action == "vault":
		if s.Enroll.Vault == nil {
			jsonResp(w, 503, map[string]string{"error": "vault integration disabled"})
			return
		}
		rec, err := s.Enroll.Vault.GetApprovedCertRecord(deviceID)
		if err != nil {
			jsonResp(w, 404, map[string]string{"error": "vault record not found"})
			return
		}
		jsonResp(w, 200, rec)

	case r.Method == "POST" && action == "approve":
		s.Enroll.ApproveDevice(deviceID)
		jsonResp(w, 200, map[string]string{"status": "approved", "device_id": deviceID})

	case r.Method == "POST" && action == "revoke":
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.Enroll.RevokeDevice(deviceID, body.Reason)
		jsonResp(w, 200, map[string]string{"status": "revoked", "device_id": deviceID})

	case r.Method == "POST" && action == "reboot":
		s.Enroll.RebootToPending(deviceID)
		jsonResp(w, 200, map[string]string{"status": "pending_approval", "device_id": deviceID})

	case r.Method == "POST" && action == "health":
		var req enrollment.HealthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResp(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		if req.DeviceID == "" {
			req.DeviceID = deviceID
		}
		if req.DeviceID != deviceID {
			jsonResp(w, 400, map[string]string{"error": "device_id mismatch"})
			return
		}
		resp := s.Enroll.HandleHealth(req)

		// Reconcile container status with actual Docker state
		if s.Simulator != nil {
			if d := s.Store.GetDevice(deviceID); d != nil {
				var names []string
				for n := range d.Containers {
					names = append(names, n)
				}
				if len(names) > 0 {
					actual := s.Simulator.ListContainerStatuses(deviceID, names)
					s.Store.SetDevice(deviceID, func(st *store.DeviceState) {
						for name, status := range actual {
							st.Containers[name] = status
						}
					})
				}
			}
		}

		statusCode := 200
		if resp.Action == "disconnect" {
			statusCode = 403
		}
		if resp.Action == "reenroll" {
			statusCode = 409
		}
		jsonResp(w, statusCode, resp)

	case r.Method == "GET" && action == "containers":
		d := s.Store.GetDevice(deviceID)
		if d == nil {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		jsonResp(w, 200, map[string]any{
			"device_id":  deviceID,
			"containers": d.Containers,
			"specs":      s.Enroll.ContainerSpecs(deviceID),
		})

	case r.Method == "DELETE" && action == "containers":
		if len(parts) < 3 {
			jsonResp(w, 400, map[string]string{"error": "container name required"})
			return
		}
		name, err := url.PathUnescape(parts[2])
		if err != nil || strings.TrimSpace(name) == "" {
			jsonResp(w, 400, map[string]string{"error": "invalid container name"})
			return
		}
		if !s.Enroll.RemoveContainer(deviceID, name) {
			jsonResp(w, 404, map[string]string{"error": "container not found"})
			return
		}
		d := s.Store.GetDevice(deviceID)
		if d != nil && s.Simulator != nil {
			if err := s.Simulator.StopContainer(deviceID, name); err != nil {
				s.Store.Emit(store.WARN, "deploy", deviceID, "Failed to stop container: "+err.Error(), "containers", map[string]any{"name": name})
			}
		}
		s.Store.Emit(store.WARN, "deploy", deviceID, "Container removed by operator: "+name, "containers", map[string]any{"name": name})
		s.Store.EmitMQTT("edgeflux/deploy/"+deviceID+"/containers/remove", map[string]any{"name": name}, deviceID, "outbound")
		jsonResp(w, 200, map[string]any{"status": "removed", "device_id": deviceID, "name": name})

	case r.Method == "POST" && action == "containers":
		d := s.Store.GetDevice(deviceID)
		if d == nil || d.ApprovalStatus == "revoked" || (d.ApprovalStatus != "approved" && !d.Simulate) || (!d.Simulate && d.CertSerial == "") {
			jsonResp(w, 409, map[string]string{"error": "device must be approved and enrolled"})
			return
		}

		var req struct {
			Name           string            `json:"name"`
			Image          string            `json:"image"`
			Ports          []string          `json:"ports"`
			Env            map[string]string `json:"env"`
			ReadOnlyRoot   bool              `json:"read_only_rootfs"`
			NoNewPrivs     bool              `json:"no_new_privileges"`
			SeccompProfile string            `json:"seccomp_profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResp(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		if req.Name == "" || req.Image == "" {
			jsonResp(w, 400, map[string]string{"error": "name and image are required"})
			return
		}

		if req.SeccompProfile == "" {
			req.SeccompProfile = "runtime/default"
		}
		custom := enrollment.Container{
			Name:           req.Name,
			Image:          req.Image,
			Ports:          req.Ports,
			Env:            req.Env,
			ReadOnlyRoot:   req.ReadOnlyRoot,
			NoNewPrivs:     req.NoNewPrivs,
			SeccompProfile: req.SeccompProfile,
		}

		containers := s.Enroll.AppendContainer(deviceID, custom)
		s.Store.Emit(store.INFO, "deploy", deviceID, "Custom container deployment requested: "+custom.Name, "containers", map[string]any{"name": custom.Name, "image": custom.Image})
		s.Store.EmitMQTT("edgeflux/deploy/"+deviceID+"/containers", containers, deviceID, "outbound")

		containerStatus := "deployed"
		var containerID string
		if s.Simulator != nil {
			cid, err := s.Simulator.DeployContainer(deviceID, custom.Name, custom.Image, custom.Ports, custom.Env)
			if err != nil {
				s.Store.Emit(store.WARN, "deploy", deviceID, "Container start failed: "+err.Error(), "containers", map[string]any{"name": custom.Name})
				containerStatus = "failed"
			} else {
				containerID = cid
				containerStatus = "running"
				s.Store.Emit(store.OK, "deploy", deviceID, "Container running: "+custom.Name, "containers", map[string]any{"name": custom.Name, "container_id": cid})
			}
		}

		s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
			d.Phase = "containers"
			d.Containers[custom.Name] = containerStatus
		})

		resp := map[string]any{"status": "queued", "device_id": deviceID, "container": custom, "total_containers": len(containers)}
		if containerID != "" {
			resp["container_id"] = containerID
			resp["status"] = "running"
		}
		jsonResp(w, 200, resp)

	case r.Method == "POST" && action == "simulate":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		d := s.Store.GetDevice(deviceID)
		if d == nil {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		var req struct {
			Profile string `json:"profile"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		containerID, err := s.Simulator.StartDevice(deviceID, req.Profile)
		if err != nil {
			s.Store.Emit(store.WARN, "simulator", deviceID, "Failed to start local simulator: "+err.Error(), "simulator", nil)
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.Store.Emit(store.OK, "simulator", deviceID, "Local simulator started", "simulator", map[string]string{"container_id": containerID})
		jsonResp(w, 200, map[string]string{"status": "started", "device_id": deviceID, "container_id": containerID})

	case r.Method == "GET" && action == "simulate":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		info, err := s.Simulator.InspectDevice(deviceID)
		if err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if info == nil {
			jsonResp(w, 200, map[string]any{"status": "not_found", "device_id": deviceID, "exec_cmd": s.Simulator.ExecCommand(deviceID)})
			return
		}
		status := "not_found"
		if info.ContainerID != "" {
			status = "found"
		}
		jsonResp(w, 200, map[string]any{
			"status":           status,
			"device_id":        deviceID,
			"name":             info.Name,
			"container_id":     info.ContainerID,
			"image":            info.Image,
			"container_status": info.Status,
			"running":          info.Running,
			"exec_cmd":         info.ExecCmd,
		})

	case r.Method == "DELETE" && action == "simulate":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		if err := s.Simulator.RemoveDevice(deviceID); err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.Store.Emit(store.INFO, "simulator", deviceID, "Local simulator removed", "simulator", nil)
		jsonResp(w, 200, map[string]string{"status": "removed", "device_id": deviceID})

	case r.Method == "POST" && action == "ssh-keys":
		d := s.Store.GetDevice(deviceID)
		if d == nil {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		var req struct {
			Comment string `json:"comment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		keyPair, err := s.Enroll.GenerateSSHKey(deviceID, req.Comment)
		if err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.Store.EmitMQTT("edgeflux/config/"+deviceID+"/ssh-keys", map[string]string{"fingerprint": keyPair.Fingerprint}, deviceID, "outbound")
		jsonResp(w, 200, map[string]any{
			"device_id":       deviceID,
			"private_key_pem": keyPair.PrivateKeyPEM,
			"public_key_ssh":  keyPair.PublicKeySSH,
			"fingerprint":     keyPair.Fingerprint,
			"comment":         keyPair.Comment,
		})

	case r.Method == "POST" && action == "ssh-connect":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		d := s.Store.GetDevice(deviceID)
		if d == nil {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		keyPair, err := s.Enroll.GenerateSSHKey(deviceID, "")
		if err != nil {
			jsonResp(w, 500, map[string]string{"error": "keygen failed: " + err.Error()})
			return
		}
		containerID, hostPort, err := s.Simulator.DeploySSHProxy(deviceID, keyPair.PublicKeySSH, 0)
		if err != nil {
			s.Store.Emit(store.WARN, "ssh", deviceID, "SSH proxy deploy failed: "+err.Error(), "ssh", nil)
			jsonResp(w, 500, map[string]string{"error": "ssh proxy failed: " + err.Error()})
			return
		}
		sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i edgeflux_%s_key edge@localhost -p %d", deviceID, hostPort)
		socksCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i edgeflux_%s_key -D 1080 -N edge@localhost -p %d", deviceID, hostPort)
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
			d.SSHConfigured = true
			d.SSHTunnel = fmt.Sprintf("localhost:%d", hostPort)
			d.Containers["sshd-proxy"] = "running"
		})
		s.Store.Emit(store.OK, "ssh", deviceID, fmt.Sprintf("SSH proxy running on port %d", hostPort), "ssh", map[string]any{"host_port": hostPort, "fingerprint": keyPair.Fingerprint})
		s.Store.EmitMQTT("edgeflux/config/"+deviceID+"/ssh-proxy", map[string]any{"host_port": hostPort, "container_id": containerID}, deviceID, "outbound")
		jsonResp(w, 200, map[string]any{
			"device_id":       deviceID,
			"status":          "connected",
			"container_id":    containerID,
			"host_port":       hostPort,
			"username":        "edge",
			"private_key_pem": keyPair.PrivateKeyPEM,
			"public_key_ssh":  keyPair.PublicKeySSH,
			"fingerprint":     keyPair.Fingerprint,
			"ssh_command":     sshCmd,
			"socks_command":   socksCmd,
		})

	case r.Method == "GET" && action == "ssh-connect":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		info, err := s.Simulator.SSHProxyInfo(deviceID)
		if err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, 200, map[string]any{
			"device_id":    deviceID,
			"running":      info.Running,
			"container_id": info.ContainerID,
			"host_port":    info.HostPort,
			"image":        info.Image,
			"status":       info.Status,
		})

	case r.Method == "DELETE" && action == "ssh-connect":
		if s.Simulator == nil {
			jsonResp(w, 503, map[string]string{"error": "local simulator is not enabled on server"})
			return
		}
		if err := s.Simulator.StopSSHProxy(deviceID); err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
			delete(d.Containers, "sshd-proxy")
			d.SSHTunnel = ""
		})
		s.Store.Emit(store.INFO, "ssh", deviceID, "SSH proxy removed", "ssh", nil)
		jsonResp(w, 200, map[string]string{"status": "removed", "device_id": deviceID})

	default:
		jsonResp(w, 404, map[string]string{"error": "unknown device action"})
	}
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/v1/deploy/{deviceID}/{type}
	path := r.URL.Path[len("/api/v1/deploy/"):]
	deviceID, typ := splitPath(path)
	if deviceID == "" {
		jsonResp(w, 400, map[string]string{"error": "device_id required"})
		return
	}

	switch typ {
	case "os":
		d := s.Store.GetDevice(deviceID)
		if d == nil || d.ApprovalStatus == "revoked" || (d.ApprovalStatus != "approved" && !d.Simulate) || (!d.Simulate && d.CertSerial == "") {
			jsonResp(w, 409, map[string]string{"error": "device must be approved and enrolled"})
			return
		}
		s.Store.Emit(store.INFO, "deploy", deviceID, "OS manifest requested", "os", nil)
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) { d.Phase = "os_deploy" })

		manifest := map[string]any{
			"image": "secureos-alpine-3.19.1-edge-hardened.img.gz", "size_bytes": 300875776,
			"sha256": "a3f8e2d1c4b5a6f7e8d9c0b1a2f3e4d5c6b7a8f9", "sig_algo": "ECDSA-SHA384",
			"dm_verity": true, "rootfs_mode": "read-only", "kernel": "6.6.10-secureos-hardened",
			"features": []string{"dm-verity", "read-only-rootfs", "tmpfs-overlays", "secure-boot", "hardened-sysctl"},
		}
		s.Store.EmitMQTT("edgeflux/deploy/"+deviceID+"/os", manifest, deviceID, "outbound")
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) { d.OSDeployed = true; d.Phase = "os" })
		s.Store.Emit(store.OK, "deploy", deviceID, "OS manifest sent", "os", nil)
		jsonResp(w, 200, manifest)

	case "containers":
		d := s.Store.GetDevice(deviceID)
		if d == nil || d.ApprovalStatus == "revoked" || (d.ApprovalStatus != "approved" && !d.Simulate) || (!d.Simulate && d.CertSerial == "") {
			jsonResp(w, 409, map[string]string{"error": "device must be approved and enrolled"})
			return
		}
		s.Store.Emit(store.INFO, "deploy", deviceID, "Container manifest requested", "containers", nil)
		containers := s.Enroll.Containers(deviceID)
		s.Store.EmitMQTT("edgeflux/deploy/"+deviceID+"/containers", containers, deviceID, "outbound")
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
			d.Phase = "containers"
			for _, c := range containers {
				d.Containers[c.Name] = "deployed"
			}
		})
		s.Store.Emit(store.OK, "deploy", deviceID, fmt.Sprintf("%d containers sent", len(containers)), "containers", nil)
		jsonResp(w, 200, map[string]any{"device_id": deviceID, "containers": containers, "registry": "registry.edgeflux.local:5443", "auth": "mtls-client-cert"})

	default:
		jsonResp(w, 400, map[string]string{"error": "unknown deploy type: " + typ})
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/v1/config/"):]
	deviceID, typ := splitPath(path)

	if typ == "ssh" {
		d := s.Store.GetDevice(deviceID)
		if d == nil || d.ApprovalStatus == "revoked" || (d.ApprovalStatus != "approved" && !d.Simulate) || (!d.Simulate && d.CertSerial == "") {
			jsonResp(w, 409, map[string]string{"error": "device must be approved and enrolled"})
			return
		}
		s.Store.Emit(store.INFO, "ssh", deviceID, "SSH config requested", "ssh", nil)
		cfg := s.Enroll.SSH(deviceID)
		s.Store.EmitMQTT("edgeflux/config/"+deviceID+"/ssh", cfg, deviceID, "outbound")
		s.Store.SetDevice(deviceID, func(d *store.DeviceState) {
			d.Phase = "ssh"
			d.SSHConfigured = true
			d.SSHTunnel = cfg.Tunnel.DeviceAlias
		})
		s.Store.Emit(store.OK, "ssh", deviceID, "SSH configured — tunnel: "+cfg.Tunnel.DeviceAlias, "ssh", nil)
		jsonResp(w, 200, cfg)
	} else {
		jsonResp(w, 400, map[string]string{"error": "unknown config type"})
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, s.Store.Stats())
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		mode := r.URL.Query().Get("mode")
		if mode == "legacy" {
			removed := s.Store.PurgeLegacyDevices()
			for _, id := range removed {
				s.Store.Emit(store.INFO, "control", id, "Legacy device removed", "cleanup", nil)
			}
			jsonResp(w, 200, map[string]any{"status": "ok", "removed": removed, "count": len(removed)})
			return
		}

		deviceID := r.URL.Query().Get("device_id")
		if deviceID == "" {
			jsonResp(w, 400, map[string]string{"error": "device_id or mode=legacy required"})
			return
		}
		if !s.Store.DeleteDevice(deviceID) {
			jsonResp(w, 404, map[string]string{"error": "device not found"})
			return
		}
		jsonResp(w, 200, map[string]string{"status": "deleted", "device_id": deviceID})
		return
	}

	if r.Method == "POST" {
		var req struct {
			DeviceID       string                `json:"device_id"`
			Profile        string                `json:"profile"`
			NICs           []store.NIC           `json:"nics"`
			AuthorizedKeys []store.AuthorizedKey `json:"authorized_keys"`
			Simulate       bool                  `json:"simulate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResp(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		if req.DeviceID == "" {
			jsonResp(w, 400, map[string]string{"error": "device_id required"})
			return
		}
		s.Enroll.CreatePendingDevice(req.DeviceID, req.Profile, req.NICs, req.AuthorizedKeys, req.Simulate || s.AutoSimOnCreate)

		resp := map[string]any{"status": "pending_approval", "device_id": req.DeviceID}
		if req.Simulate || s.AutoSimOnCreate {
			if s.Simulator == nil {
				resp["simulate_status"] = "unavailable"
			} else {
				containerID, err := s.Simulator.StartDevice(req.DeviceID, req.Profile)
				if err != nil {
					s.Store.Emit(store.WARN, "simulator", req.DeviceID, "Failed to start local simulator: "+err.Error(), "simulator", nil)
					resp["simulate_status"] = "failed"
					resp["simulate_error"] = err.Error()
				} else {
					s.Store.Emit(store.OK, "simulator", req.DeviceID, "Local simulator started", "simulator", map[string]string{"container_id": containerID})
					resp["simulate_status"] = "started"
					resp["simulator_container_id"] = containerID
				}
			}
		}

		jsonResp(w, 201, resp)
		return
	}
	jsonResp(w, 200, s.Store.AllDevices())
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 200
	}
	jsonResp(w, 200, s.Store.Events(after, limit))
}

func (s *Server) handlePKI(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{}
	if s.PKI.RootCA != nil {
		info["root_ca"] = map[string]string{
			"subject": s.PKI.RootCA.Cert.Subject.String(), "serial": s.PKI.RootCA.Serial,
			"thumbprint": s.PKI.RootCA.Thumbprint, "not_after": s.PKI.RootCA.Cert.NotAfter.Format(time.RFC3339),
		}
	}
	if s.PKI.IntCA != nil {
		info["intermediate_ca"] = map[string]string{
			"subject": s.PKI.IntCA.Cert.Subject.String(), "serial": s.PKI.IntCA.Serial,
			"thumbprint": s.PKI.IntCA.Thumbprint, "issuer": s.PKI.IntCA.Cert.Issuer.String(),
		}
	}
	jsonResp(w, 200, info)
}

func splitPath(p string) (string, string) {
	for i, c := range p {
		if c == '/' {
			return p[:i], p[i+1:]
		}
	}
	return p, ""
}

func jsonResp(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleSSHTerminal(w http.ResponseWriter, r *http.Request) {
	// Parse device ID from /api/v1/ssh-terminal/{deviceID}
	deviceID := strings.TrimPrefix(r.URL.Path, "/api/v1/ssh-terminal/")
	deviceID = strings.Trim(deviceID, "/")
	if deviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}
	if s.Simulator == nil {
		http.Error(w, "simulator not enabled", http.StatusServiceUnavailable)
		return
	}

	// Get SSH proxy info
	info, err := s.Simulator.SSHProxyInfo(deviceID)
	if err != nil || !info.Running || info.HostPort == 0 {
		http.Error(w, "SSH proxy not running for this device — start it first", http.StatusConflict)
		return
	}

	// Generate a temporary SSH key for this session
	keyPair, err := s.Enroll.GenerateSSHKey(deviceID, "web-terminal")
	if err != nil {
		http.Error(w, "keygen failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-deploy the SSH proxy with the new key so it's authorized
	_, _, err = s.Simulator.DeploySSHProxy(deviceID, keyPair.PublicKeySSH, info.HostPort)
	if err != nil {
		http.Error(w, "failed to update ssh proxy: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Brief pause for sshd to start in new container
	time.Sleep(2 * time.Second)

	// Parse the private key for SSH dial
	signer, err := ssh.ParsePrivateKey([]byte(keyPair.PrivateKeyPEM))
	if err != nil {
		http.Error(w, "parse key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Determine SSH target — from server container, dial the host-mapped port
	sshHost := fmt.Sprintf("host.docker.internal:%d", info.HostPort)
	// Fallback: try localhost if not in Docker
	sshClient, err := ssh.Dial("tcp", sshHost, &ssh.ClientConfig{
		User:            "edge",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		// Try localhost
		sshHost = fmt.Sprintf("localhost:%d", info.HostPort)
		sshClient, err = ssh.Dial("tcp", sshHost, &ssh.ClientConfig{
			User:            "edge",
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		})
		if err != nil {
			// Try container's network address directly
			sshHost = fmt.Sprintf("%s:2222", s.sshProxyContainerIP(deviceID))
			sshClient, err = ssh.Dial("tcp", sshHost, &ssh.ClientConfig{
				User:            "edge",
				Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         5 * time.Second,
			})
			if err != nil {
				http.Error(w, "ssh dial failed: "+err.Error(), http.StatusBadGateway)
				return
			}
		}
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		http.Error(w, "ssh session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer session.Close()

	// Request pty
	if err := session.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{
		ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		http.Error(w, "pty: "+err.Error(), http.StatusInternalServerError)
		return
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		http.Error(w, "stdin: "+err.Error(), http.StatusInternalServerError)
		return
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		http.Error(w, "stdout: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := session.Shell(); err != nil {
		http.Error(w, "shell: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Upgrade to WebSocket
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("websocket accept failed: %v", err)
		return
	}
	defer ws.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	s.Store.Emit(store.OK, "ssh", deviceID, "Web SSH terminal opened", "ssh", nil)

	// WS → SSH stdin
	go func() {
		defer cancel()
		for {
			_, msg, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if _, err := stdinPipe.Write(msg); err != nil {
				return
			}
		}
	}()

	// SSH stdout → WS
	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				if wErr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait for session to end or context cancel
	done := make(chan struct{})
	go func() {
		_ = session.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	s.Store.Emit(store.INFO, "ssh", deviceID, "Web SSH terminal closed", "ssh", nil)
	ws.Close(websocket.StatusNormalClosure, "session ended")
}

// sshProxyContainerIP looks up the container's IP on the Docker network.
func (s *Server) sshProxyContainerIP(deviceID string) string {
	// Try to resolve the container name via DNS (works inside Docker network)
	name := "edgeflux-sim-" + deviceID + "-sshd"
	addrs, err := net.LookupHost(name)
	if err == nil && len(addrs) > 0 {
		return addrs[0]
	}
	return "localhost"
}

func (s *Server) Start(addr string) error {
	log.Printf("API server: http://localhost%s", addr)
	log.Printf("Dashboard:  http://localhost%s", addr)
	log.Printf("SSE stream: http://localhost%s/events", addr)
	return http.ListenAndServe(addr, s.mux)
}
