package devicedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/edgeflux/edgeflux/internal/store"
)

type DB struct {
	sql *sql.DB
	mu  sync.Mutex
}

const upsertDeviceSQL = `
INSERT INTO devices (
	device_id, phase, status, approval_status, cert_serial, cert_thumbprint, cert_not_after,
	connection_alive, health_messages, last_health, last_seen,
	nics_json, authorized_keys_json, containers_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(device_id) DO UPDATE SET
	phase=excluded.phase,
	status=excluded.status,
	approval_status=excluded.approval_status,
	cert_serial=excluded.cert_serial,
	cert_thumbprint=excluded.cert_thumbprint,
	cert_not_after=excluded.cert_not_after,
	connection_alive=excluded.connection_alive,
	health_messages=excluded.health_messages,
	last_health=excluded.last_health,
	last_seen=excluded.last_seen,
	nics_json=excluded.nics_json,
	authorized_keys_json=excluded.authorized_keys_json,
	containers_json=excluded.containers_json,
	updated_at=excluded.updated_at
`

func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("db path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	d := &DB{sql: db}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	return d.sql.Close()
}

func (d *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS devices (
  device_id TEXT PRIMARY KEY,
  phase TEXT,
  status TEXT,
  approval_status TEXT,
  cert_serial TEXT,
  cert_thumbprint TEXT,
  cert_not_after TEXT,
  connection_alive INTEGER,
  health_messages INTEGER,
  last_health TEXT,
  last_seen TEXT,
  nics_json TEXT,
  authorized_keys_json TEXT,
  containers_json TEXT,
  updated_at TEXT
);
`
	_, err := d.sql.Exec(schema)
	return err
}

func (d *DB) UpsertDevice(st store.DeviceState) error {
	return d.UpsertDevicesBatch([]store.DeviceState{st})
}

func (d *DB) UpsertDevicesBatch(states []store.DeviceState) error {
	if len(states) == 0 {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		err := d.upsertBatchTx(states)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isBusyErr(err) {
			return err
		}
		time.Sleep(time.Duration(25*(attempt+1)) * time.Millisecond)
	}
	return lastErr
}

func (d *DB) upsertBatchTx(states []store.DeviceState) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(upsertDeviceSQL)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, st := range states {
		args, err := deviceArgs(st)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := stmt.Exec(args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

func deviceArgs(st store.DeviceState) ([]any, error) {
	nics, _ := json.Marshal(st.NICs)
	keys, _ := json.Marshal(st.AuthorizedKeys)
	containers, _ := json.Marshal(st.Containers)

	var certNotAfter any
	if st.CertNotAfter != nil {
		certNotAfter = st.CertNotAfter.UTC().Format(time.RFC3339)
	}
	var lastHealth any
	if st.LastHealth != nil {
		lastHealth = st.LastHealth.UTC().Format(time.RFC3339)
	}
	lastSeen := st.LastSeen.UTC().Format(time.RFC3339)

	args := []any{
		st.DeviceID,
		st.Phase,
		st.Status,
		st.ApprovalStatus,
		st.CertSerial,
		st.CertThumbprint,
		certNotAfter,
		boolToInt(st.ConnectionAlive),
		st.HealthCount,
		lastHealth,
		lastSeen,
		string(nics),
		string(keys),
		string(containers),
		time.Now().UTC().Format(time.RFC3339),
	}
	return args, nil
}

func (d *DB) DeleteLegacyDevices() (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.execWithRetry(`
DELETE FROM devices
WHERE (nics_json IS NULL OR nics_json = '' OR nics_json = 'null' OR nics_json = '[]')
  AND (authorized_keys_json IS NULL OR authorized_keys_json = '' OR authorized_keys_json = 'null' OR authorized_keys_json = '[]')
`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) execWithRetry(query string, args ...any) (sql.Result, error) {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		res, err := d.sql.Exec(query, args...)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isBusyErr(err) {
			return nil, err
		}
		time.Sleep(time.Duration(25*(attempt+1)) * time.Millisecond)
	}
	return nil, lastErr
}

func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
