package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	server := env("SERVER_URL", "http://localhost:8080")
	deviceID := env("DEVICE_ID", genID())
	profile := env("PROFILE", "alpine-edge-secure")

	log.Println("═══════════════════════════════════════")
	log.Println(" EdgeFlux Agent — Zero-Touch Enrollment")
	log.Printf(" Device:  %s", deviceID)
	log.Printf(" Server:  %s", server)
	log.Printf(" Profile: %s", profile)
	log.Println("═══════════════════════════════════════")

	for {
		certSerial, ok := runEnrollment(server, deviceID, profile)
		if !ok {
			return
		}

		action := runHealthLoop(server, deviceID, certSerial)
		if action == "disconnect" {
			log.Println("Device disconnected by server policy (revoked). Attempting re-enrollment...")
			time.Sleep(3 * time.Second)
			continue
		}

		log.Println("Server requested re-enrollment. Restarting enrollment workflow...")
		time.Sleep(2 * time.Second)
	}
}

func runEnrollment(server, deviceID, profile string) (string, bool) {
	// Phase 1: Generate identity
	log.Println("\n[1/7] Generating device keypair (EC P-256)...")
	key, csrPEM := mustCSR(deviceID)
	_ = key
	log.Printf("[1/7] CSR ready — CN=%s.edge.edgeflux.local", deviceID)

	// Phase 2: Hardware attestation
	log.Println("\n[2/7] Collecting hardware attestation...")
	hwAtt := "TPM2.0-EK:" + hexHash(deviceID + "hw")[:32]
	fwHash := "sha256:" + hexHash(deviceID+"fw")
	log.Printf("[2/7] HW: %s", hwAtt)
	log.Printf("[2/7] FW: %s", fwHash)

	var resp map[string]any
	for {
		log.Println("\n[3/7] Sending enrollment request...")
		body, _ := json.Marshal(map[string]string{
			"device_id": deviceID, "csr_pem": string(csrPEM),
			"hw_attestation": hwAtt, "firmware_hash": fwHash, "profile": profile,
		})
		code, out := postJSON(server+"/api/v1/enroll", body)
		if code == 202 {
			log.Printf("[3/7] Enrollment pending approval: %v", out["next_step"])
			time.Sleep(3 * time.Second)
			continue
		}
		if code == 403 {
			log.Printf("[3/7] Enrollment rejected (likely revoked): %v", out["error"])
			time.Sleep(3 * time.Second)
			continue
		}
		if code != 200 {
			log.Printf("[3/7] Enrollment failed: HTTP %d %v", code, out)
			time.Sleep(3 * time.Second)
			continue
		}
		resp = out
		break
	}

	log.Printf("[3/7] Status: %s", resp["status"])
	log.Printf("[3/7] Cert serial: %s", resp["cert_serial"])
	log.Printf("[3/7] Next: %s", resp["next_step"])

	certSerial := toString(resp["cert_serial"])

	if certPEM, ok := resp["cert_pem"].(string); ok && certPEM != "" {
		os.MkdirAll("/tmp/edgeflux-agent/certs", 0700)
		_ = os.WriteFile("/tmp/edgeflux-agent/certs/device.pem", []byte(certPEM), 0644)
		log.Println("[3/7] Device certificate saved")
	}

	// Phase 4: mTLS (in production: reconnect with device cert)
	log.Println("\n[4/7] mTLS — in production, reconnects with device cert")
	log.Printf("[4/7] Thumbprint: %s", resp["cert_thumbprint"])
	time.Sleep(300 * time.Millisecond)

	// Phase 5: OS manifest
	log.Println("\n[5/7] Requesting OS deployment manifest...")
	osM := mustGet(server + "/api/v1/deploy/" + deviceID + "/os")
	prettyLog("[5/7] OS manifest", osM)
	log.Println("[5/7] In production: download + flash image via mTLS")
	time.Sleep(300 * time.Millisecond)

	// Phase 6: Containers
	log.Println("\n[6/7] Requesting container manifest...")
	ctM := mustGet(server + "/api/v1/deploy/" + deviceID + "/containers")
	if containers, ok := ctM["containers"].([]any); ok {
		for _, c := range containers {
			if cm, ok := c.(map[string]any); ok {
				log.Printf("[6/7] Pulling %s → %s", cm["name"], cm["image"])
				time.Sleep(200 * time.Millisecond)
				log.Printf("[6/7] %s: running", cm["name"])
			}
		}
	}

	// Phase 7: SSH
	log.Println("\n[7/7] Requesting SSH configuration...")
	sshM := mustGet(server + "/api/v1/config/" + deviceID + "/ssh")
	if keys, ok := sshM["authorized_keys"].([]any); ok {
		for _, k := range keys {
			if km, ok := k.(map[string]any); ok {
				log.Printf("[7/7] SSH key: %s → %s", km["comment"], km["access_level"])
			}
		}
	}
	if tun, ok := sshM["tunnel"].(map[string]any); ok {
		log.Printf("[7/7] Tunnel: ssh %s (via %s:%v)", tun["device_alias"], tun["relay_host"], tun["relay_port"])
	}

	log.Println("\n═══════════════════════════════════════")
	log.Printf(" Device %s: FULLY PROVISIONED", deviceID)
	log.Println("═══════════════════════════════════════")

	return certSerial, true
}

func runHealthLoop(server, deviceID, certSerial string) string {
	log.Println("Starting health telemetry loop (every 3s)...")
	start := time.Now()
	for {
		uptime := int64(time.Since(start).Seconds())
		cpu := 22.0 + 11*math.Sin(float64(uptime)/8.0)
		mem := 41.0 + 7*math.Cos(float64(uptime)/10.0)

		payload, _ := json.Marshal(map[string]any{
			"device_id":          deviceID,
			"cert_serial":        certSerial,
			"status":             "online",
			"cpu_percent":        round(cpu, 1),
			"mem_percent":        round(mem, 1),
			"uptime_seconds":     uptime,
			"running_containers": detectRunningContainers(),
		})

		code, resp := postJSON(server+"/api/v1/devices/"+deviceID+"/health", payload)
		action := toString(resp["action"])
		if code == 403 || action == "disconnect" {
			log.Printf("Health rejected: %v", resp["message"])
			return "disconnect"
		}
		if code == 409 || action == "reenroll" {
			log.Printf("Health response requires re-enroll: %v", resp["message"])
			return "reenroll"
		}
		if code != 200 {
			log.Printf("Health push failed HTTP %d: %v", code, resp)
		}

		log.Printf("[health] cpu=%.1f%% mem=%.1f%% uptime=%ds", round(cpu, 1), round(mem, 1), uptime)
		time.Sleep(3 * time.Second)
	}
}

func mustCSR(id string) (*ecdsa.PrivateKey, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: id + ".edge.edgeflux.local", Organization: []string{"EdgeFlux Systems"}},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		log.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func mustPost(url string, body []byte) map[string]any {
	code, out := postJSON(url, body)
	if code != 200 {
		log.Fatalf("POST %s: HTTP %d: %v", url, code, out)
	}
	return out
}

func postJSON(url string, body []byte) (int, map[string]any) {
	r, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("POST %s: %v", url, err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if out == nil {
		out = map[string]any{}
	}
	return r.StatusCode, out
}

func mustGet(url string) map[string]any {
	r, err := http.Get(url)
	if err != nil {
		log.Fatalf("GET %s: %v", url, err)
	}
	defer r.Body.Close()
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	return out
}

func prettyLog(label string, v any) {
	d, _ := json.MarshalIndent(v, "  ", "  ")
	log.Printf("%s:\n  %s", label, d)
}

func hexHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func genID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("edge-%s", hex.EncodeToString(b))
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func detectRunningContainers() []string {
	if configured := strings.TrimSpace(os.Getenv("RUNNING_CONTAINERS")); configured != "" {
		return splitCSV(configured)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		return []string{"edgeflux-agent", "mqtt-broker", "health-monitor", "log-forwarder"}
	}

	lines := strings.Split(string(out), "\n")
	names := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return []string{"edgeflux-agent"}
	}
	return names
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"edgeflux-agent"}
	}
	return out
}
