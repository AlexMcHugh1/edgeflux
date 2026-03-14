package store

import (
	"encoding/json"
	"sync"
	"time"
)

type Level string

const (
	INFO  Level = "INFO"
	OK    Level = "OK"
	WARN  Level = "WARN"
	ERROR Level = "ERROR"
	MQTT  Level = "MQTT"
	TLS   Level = "TLS"
)

type Event struct {
	ID        int       `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     Level     `json:"level"`
	Source    string    `json:"source"`
	DeviceID  string    `json:"device_id,omitempty"`
	Message   string    `json:"message"`
	Topic     string    `json:"topic,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	Data      any       `json:"data,omitempty"`
}

type NIC struct {
	Name  string `json:"name"`
	MAC   string `json:"mac,omitempty"`
	IP    string `json:"ip,omitempty"`
	CIDR  string `json:"cidr,omitempty"`
	VLAN  int    `json:"vlan,omitempty"`
	State string `json:"state,omitempty"`
}

type AuthorizedKey struct {
	Comment string `json:"comment,omitempty"`
	PubKey  string `json:"public_key"`
	Access  string `json:"access_level,omitempty"`
}

type DeviceState struct {
	DeviceID         string            `json:"device_id"`
	Phase            string            `json:"phase"`
	Status           string            `json:"status"`
	ApprovalStatus   string            `json:"approval_status,omitempty"`
	ApprovedAt       *time.Time        `json:"approved_at,omitempty"`
	RevokedAt        *time.Time        `json:"revoked_at,omitempty"`
	RevocationReason string            `json:"revocation_reason,omitempty"`
	EnrolledAt       *time.Time        `json:"enrolled_at,omitempty"`
	CertSerial       string            `json:"cert_serial,omitempty"`
	CertThumbprint   string            `json:"cert_thumbprint,omitempty"`
	CertNotAfter     *time.Time        `json:"cert_not_after,omitempty"`
	NICs             []NIC             `json:"nics,omitempty"`
	AuthorizedKeys   []AuthorizedKey   `json:"authorized_keys,omitempty"`
	OSDeployed       bool              `json:"os_deployed"`
	Containers       map[string]string `json:"containers"`
	SSHConfigured    bool              `json:"ssh_configured"`
	SSHTunnel        string            `json:"ssh_tunnel,omitempty"`
	MTLSEstablished  bool              `json:"mtls_established"`
	Simulate         bool              `json:"simulate"`
	LastSeen         time.Time         `json:"last_seen"`
	LastHealth       *time.Time        `json:"last_health,omitempty"`
	HealthCount      int               `json:"health_messages"`
	ConnectionAlive  bool              `json:"connection_alive"`
	MQTTCount        int               `json:"mqtt_messages"`
}

type EventStore struct {
	mu             sync.RWMutex
	events         []Event
	devices        map[string]*DeviceState
	subs           []chan Event
	nextID         int
	onDeviceUpdate func(DeviceState)
}

func New() *EventStore {
	return &EventStore{devices: make(map[string]*DeviceState)}
}

func (s *EventStore) Emit(level Level, source, deviceID, message, phase string, data any) Event {
	s.mu.Lock()
	s.nextID++
	e := Event{ID: s.nextID, Timestamp: time.Now(), Level: level, Source: source, DeviceID: deviceID, Message: message, Phase: phase, Data: data}
	s.events = append(s.events, e)
	subs := make([]chan Event, len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
	return e
}

func (s *EventStore) EmitMQTT(topic string, payload any, deviceID, direction string) {
	s.mu.Lock()
	s.nextID++
	e := Event{ID: s.nextID, Timestamp: time.Now(), Level: MQTT, Source: "mqtts", DeviceID: deviceID, Message: direction + " " + topic, Topic: topic, Data: payload}
	s.events = append(s.events, e)
	subs := make([]chan Event, len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *EventStore) SetDevice(deviceID string, fn func(*DeviceState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok {
		d = &DeviceState{
			DeviceID:       deviceID,
			Phase:          "pending",
			Status:         "pending_approval",
			ApprovalStatus: "pending",
			Containers:     make(map[string]string),
			LastSeen:       time.Now(),
		}
		s.devices[deviceID] = d
	}
	fn(d)
	d.LastSeen = time.Now()

	if s.onDeviceUpdate != nil {
		cp := cloneDeviceState(d)
		go s.onDeviceUpdate(cp)
	}
}

func (s *EventStore) GetDevice(id string) *DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if d, ok := s.devices[id]; ok {
		cp := cloneDeviceState(d)
		return &cp
	}
	return nil
}

func (s *EventStore) AllDevices() []DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DeviceState, 0, len(s.devices))
	for _, d := range s.devices {
		cp := cloneDeviceState(d)
		out = append(out, cp)
	}
	return out
}

func (s *EventStore) SetDeviceUpdateHook(fn func(DeviceState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDeviceUpdate = fn
}

func (s *EventStore) DeleteDevice(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return false
	}
	delete(s.devices, deviceID)
	return true
}

func (s *EventStore) PurgeLegacyDevices() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := make([]string, 0)
	for id, d := range s.devices {
		if len(d.NICs) == 0 && len(d.AuthorizedKeys) == 0 {
			delete(s.devices, id)
			removed = append(removed, id)
		}
	}
	return removed
}

func cloneDeviceState(d *DeviceState) DeviceState {
	cp := *d
	if d.Containers != nil {
		cp.Containers = make(map[string]string, len(d.Containers))
		for k, v := range d.Containers {
			cp.Containers[k] = v
		}
	}
	if d.NICs != nil {
		cp.NICs = append([]NIC(nil), d.NICs...)
	}
	if d.AuthorizedKeys != nil {
		cp.AuthorizedKeys = append([]AuthorizedKey(nil), d.AuthorizedKeys...)
	}
	return cp
}

func (s *EventStore) DeviceEvents(deviceID string, limit int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	out := make([]Event, 0, limit)
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.DeviceID == deviceID {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *EventStore) Events(after, limit int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Event
	for _, e := range s.events {
		if e.ID > after {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (s *EventStore) Subscribe() chan Event {
	ch := make(chan Event, 128)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	return ch
}

func (s *EventStore) Unsubscribe(ch chan Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.subs {
		if c == ch {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (s *EventStore) Stats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enrolled, operational, mqttCount := 0, 0, 0
	pending, revoked, connected := 0, 0, 0
	for _, d := range s.devices {
		if d.CertSerial != "" {
			enrolled++
		}
		if d.Phase == "complete" || d.Phase == "ssh" {
			operational++
		}
		if d.ApprovalStatus == "pending" || d.Status == "pending_approval" {
			pending++
		}
		if d.ApprovalStatus == "revoked" {
			revoked++
		}
		if d.ConnectionAlive {
			connected++
		}
	}
	for _, e := range s.events {
		if e.Level == MQTT {
			mqttCount++
		}
	}
	return map[string]any{
		"total_events":  len(s.events),
		"total_mqtt":    mqttCount,
		"total_devices": len(s.devices),
		"enrolled":      enrolled,
		"operational":   operational,
		"pending":       pending,
		"revoked":       revoked,
		"connected":     connected,
	}
}

func (e Event) JSON() []byte {
	b, _ := json.Marshal(e)
	return b
}
