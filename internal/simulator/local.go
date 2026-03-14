package simulator

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LocalConfig struct {
	DockerCommand string
	Image         string
	ServerURL     string
	Network       string
	NamePrefix    string
}

type Local struct {
	cfg LocalConfig
}

type ContainerInfo struct {
	Name        string `json:"name"`
	ContainerID string `json:"container_id,omitempty"`
	Image       string `json:"image,omitempty"`
	Status      string `json:"status,omitempty"`
	Running     bool   `json:"running"`
	ExecCmd     string `json:"exec_cmd"`
}

type SSHProxyInfo struct {
	ContainerID string `json:"container_id,omitempty"`
	Image       string `json:"image,omitempty"`
	Status      string `json:"status,omitempty"`
	Running     bool   `json:"running"`
	HostPort    int    `json:"host_port,omitempty"`
}

func NewLocal(cfg LocalConfig) *Local {
	if cfg.DockerCommand == "" {
		cfg.DockerCommand = "docker"
	}
	if cfg.Image == "" {
		cfg.Image = "edgeflux-edgeflux-agent:latest"
	}
	if cfg.ServerURL == "" {
		cfg.ServerURL = "http://host.docker.internal:8080"
	}
	if cfg.NamePrefix == "" {
		cfg.NamePrefix = "edgeflux-sim-"
	}
	return &Local{cfg: cfg}
}

func (l *Local) StartDevice(deviceID, profile string) (string, error) {
	if deviceID == "" {
		return "", fmt.Errorf("device_id required")
	}
	if profile == "" {
		profile = "alpine-edge-secure"
	}
	if _, err := exec.LookPath(l.cfg.DockerCommand); err != nil {
		return "", fmt.Errorf("docker command not found: %w", err)
	}

	name := l.cfg.NamePrefix + safeName(deviceID)
	_, _ = l.run(5*time.Second, "rm", "-f", name)

	args := []string{
		"run", "-d", "--rm", "--name", name,
		"-e", "DEVICE_ID=" + deviceID,
		"-e", "SERVER_URL=" + l.cfg.ServerURL,
		"-e", "PROFILE=" + profile,
		"--label", "edgeflux.simulator=true",
	}
	if l.cfg.Network != "" {
		args = append(args, "--network", l.cfg.Network)
	}
	args = append(args, l.cfg.Image)

	out, err := l.run(20*time.Second, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (l *Local) InspectDevice(deviceID string) (*ContainerInfo, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("device_id required")
	}
	name := l.cfg.NamePrefix + safeName(deviceID)
	info := &ContainerInfo{Name: name, ExecCmd: l.ExecCommand(deviceID)}

	out, err := l.run(8*time.Second, "inspect", "--format", "{{.Id}}|{{.Config.Image}}|{{.State.Status}}", name)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such object") {
			return info, nil
		}
		return nil, err
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) >= 3 {
		info.ContainerID = strings.TrimSpace(parts[0])
		info.Image = strings.TrimSpace(parts[1])
		info.Status = strings.TrimSpace(parts[2])
		info.Running = info.Status == "running"
	}
	return info, nil
}

func (l *Local) RemoveDevice(deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("device_id required")
	}
	name := l.cfg.NamePrefix + safeName(deviceID)
	_, err := l.run(8*time.Second, "rm", "-f", name)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such container") || strings.Contains(msg, "no such object") {
			return nil
		}
		return err
	}
	return nil
}

func (l *Local) ExecCommand(deviceID string) string {
	name := l.cfg.NamePrefix + safeName(deviceID)
	return fmt.Sprintf("docker exec -it %s sh", name)
}

func (l *Local) containerName(deviceID, name string) string {
	return l.cfg.NamePrefix + safeName(deviceID) + "-" + safeName(name)
}

func (l *Local) DeployContainer(deviceID, name, image string, ports []string, env map[string]string) (string, error) {
	if deviceID == "" || name == "" || image == "" {
		return "", fmt.Errorf("device_id, name, and image are required")
	}
	if _, err := exec.LookPath(l.cfg.DockerCommand); err != nil {
		return "", fmt.Errorf("docker command not found: %w", err)
	}

	cName := l.containerName(deviceID, name)
	_, _ = l.run(5*time.Second, "rm", "-f", cName)

	args := []string{
		"run", "-d", "--name", cName,
		"--label", "edgeflux.simulator=true",
		"--label", "edgeflux.device=" + deviceID,
		"--label", "edgeflux.container=" + name,
	}
	if l.cfg.Network != "" {
		args = append(args, "--network", l.cfg.Network)
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	for _, p := range ports {
		args = append(args, "--expose", strings.TrimSuffix(strings.TrimSuffix(p, "/tcp"), "/udp"))
	}
	args = append(args, image)

	out, err := l.run(30*time.Second, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (l *Local) StopContainer(deviceID, name string) error {
	if deviceID == "" || name == "" {
		return fmt.Errorf("device_id and name are required")
	}
	cName := l.containerName(deviceID, name)
	_, err := l.run(10*time.Second, "rm", "-f", cName)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such container") || strings.Contains(msg, "no such object") {
			return nil
		}
		return err
	}
	return nil
}

func (l *Local) sshProxyName(deviceID string) string {
	return l.cfg.NamePrefix + safeName(deviceID) + "-sshd"
}

func (l *Local) DeploySSHProxy(deviceID, publicKey string, sshPort int) (string, int, error) {
	if deviceID == "" || publicKey == "" {
		return "", 0, fmt.Errorf("device_id and publicKey required")
	}
	if _, err := exec.LookPath(l.cfg.DockerCommand); err != nil {
		return "", 0, fmt.Errorf("docker command not found: %w", err)
	}
	if sshPort <= 0 {
		sshPort = 0 // docker will assign a random port
	}

	cName := l.sshProxyName(deviceID)
	_, _ = l.run(5*time.Second, "rm", "-f", cName)

	portMap := "2222"
	if sshPort > 0 {
		portMap = strconv.Itoa(sshPort) + ":2222"
	}

	args := []string{
		"run", "-d", "--name", cName,
		"-p", portMap,
		"-e", "PUID=1000",
		"-e", "PGID=1000",
		"-e", "TZ=Etc/UTC",
		"-e", "SUDO_ACCESS=true",
		"-e", "PASSWORD_ACCESS=false",
		"-e", "USER_NAME=edge",
		"-e", "PUBLIC_KEY=" + publicKey,
		"--label", "edgeflux.simulator=true",
		"--label", "edgeflux.device=" + deviceID,
		"--label", "edgeflux.ssh-proxy=true",
	}
	if l.cfg.Network != "" {
		args = append(args, "--network", l.cfg.Network)
	}
	args = append(args, "lscr.io/linuxserver/openssh-server:latest")

	out, err := l.run(60*time.Second, args...)
	if err != nil {
		return "", 0, err
	}
	containerID := strings.TrimSpace(out)

	// Discover assigned host port
	hostPort := sshPort
	portOut, err := l.run(5*time.Second, "port", cName, "2222")
	if err == nil {
		// Output like "0.0.0.0:32768"
		portStr := strings.TrimSpace(portOut)
		if idx := strings.LastIndex(portStr, ":"); idx >= 0 {
			if p, err := strconv.Atoi(portStr[idx+1:]); err == nil {
				hostPort = p
			}
		}
	}

	return containerID, hostPort, nil
}

func (l *Local) StopSSHProxy(deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("device_id required")
	}
	cName := l.sshProxyName(deviceID)
	_, err := l.run(10*time.Second, "rm", "-f", cName)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such container") || strings.Contains(msg, "no such object") {
			return nil
		}
		return err
	}
	return nil
}

func (l *Local) SSHProxyInfo(deviceID string) (*SSHProxyInfo, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("device_id required")
	}
	cName := l.sshProxyName(deviceID)
	info := &SSHProxyInfo{}

	out, err := l.run(8*time.Second, "inspect", "--format", "{{.Id}}|{{.Config.Image}}|{{.State.Status}}", cName)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such object") || strings.Contains(msg, "no such container") {
			return info, nil
		}
		return nil, err
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) >= 3 {
		info.ContainerID = strings.TrimSpace(parts[0])
		info.Image = strings.TrimSpace(parts[1])
		info.Status = strings.TrimSpace(parts[2])
		info.Running = info.Status == "running"
	}

	portOut, err := l.run(5*time.Second, "port", cName, "2222")
	if err == nil {
		portStr := strings.TrimSpace(portOut)
		if idx := strings.LastIndex(portStr, ":"); idx >= 0 {
			if p, err := strconv.Atoi(portStr[idx+1:]); err == nil {
				info.HostPort = p
			}
		}
	}

	return info, nil
}

func (l *Local) ListContainerStatuses(deviceID string, containerNames []string) map[string]string {
	result := make(map[string]string, len(containerNames))
	for _, name := range containerNames {
		cName := l.containerName(deviceID, name)
		out, err := l.run(5*time.Second, "inspect", "--format", "{{.State.Status}}", cName)
		if err != nil {
			continue // container doesn't exist or can't be inspected
		}
		status := strings.TrimSpace(out)
		if status != "" {
			result[name] = status
		}
	}
	return result
}

func (l *Local) run(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, l.cfg.DockerCommand, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w (%s)", l.cfg.DockerCommand, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

var nonSafe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func safeName(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = nonSafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return "device"
	}
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
