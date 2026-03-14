package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/edgeflux/edgeflux/internal/api"
	"github.com/edgeflux/edgeflux/internal/devicedb"
	"github.com/edgeflux/edgeflux/internal/enrollment"
	"github.com/edgeflux/edgeflux/internal/pki"
	"github.com/edgeflux/edgeflux/internal/simulator"
	"github.com/edgeflux/edgeflux/internal/store"
	"github.com/edgeflux/edgeflux/internal/vault"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Println("EdgeFlux Server starting...")

	certsDir := env("CERTS_DIR", "./certs")
	addr := env("LISTEN_ADDR", ":8080")

	es := store.New()
	es.Emit(store.INFO, "server", "", "Initializing EdgeFlux platform", "boot", nil)

	dbPath := env("DEVICE_DB_PATH", "./data/edgeflux.db")
	deviceDB, err := devicedb.Open(dbPath)
	if err != nil {
		log.Fatalf("Device DB init failed: %v", err)
	}
	defer deviceDB.Close()
	es.Emit(store.OK, "db", "", "SQLite device DB ready: "+dbPath, "boot", nil)
	if removed, err := deviceDB.DeleteLegacyDevices(); err != nil {
		es.Emit(store.WARN, "db", "", "Legacy device cleanup failed: "+err.Error(), "boot", nil)
	} else if removed > 0 {
		es.Emit(store.INFO, "db", "", "Removed legacy pre-metadata device rows: "+strconv.FormatInt(removed, 10), "boot", nil)
	}
	deviceWriter := devicedb.NewWriter(deviceDB, devicedb.WriterConfig{
		QueueSize:      4096,
		FlushInterval:  120 * time.Millisecond,
		ImmediateBatch: 256,
	}, func(err error, batchSize int) {
		es.Emit(store.WARN, "db", "", "SQLite batch upsert failed: "+err.Error(), "db", map[string]any{"batch_size": batchSize})
	})
	defer deviceWriter.Close()
	es.SetDeviceUpdateHook(func(d store.DeviceState) {
		deviceWriter.Enqueue(d)
	})

	// Bootstrap PKI
	pm := pki.NewManager(certsDir)
	ttlMinutes := envInt("DEVICE_CERT_TTL_MINUTES", 10)
	pm.SetDeviceCertTTL(time.Duration(ttlMinutes) * time.Minute)
	es.Emit(store.INFO, "pki", "", "Device certificate TTL set to "+strconv.Itoa(ttlMinutes)+"m", "pki", nil)

	es.Emit(store.INFO, "pki", "", "Generating Root CA (EC P-384)", "pki", nil)
	root, err := pm.BootstrapRootCA()
	if err != nil {
		log.Fatalf("Root CA failed: %v", err)
	}
	es.Emit(store.OK, "pki", "", "Root CA — serial="+root.Serial, "pki",
		map[string]string{"serial": root.Serial, "thumbprint": root.Thumbprint})

	es.Emit(store.INFO, "pki", "", "Generating Enrollment CA (EC P-384)", "pki", nil)
	intCA, err := pm.BootstrapIntermediateCA()
	if err != nil {
		log.Fatalf("Intermediate CA failed: %v", err)
	}
	es.Emit(store.OK, "pki", "", "Enrollment CA — serial="+intCA.Serial, "pki",
		map[string]string{"serial": intCA.Serial, "thumbprint": intCA.Thumbprint})

	es.Emit(store.INFO, "pki", "", "Generating server TLS cert", "pki", nil)
	srv, err := pm.GenerateServerCert([]string{"localhost", "edgeflux-server", "gateway.edgeflux.local", "127.0.0.1", "0.0.0.0"})
	if err != nil {
		log.Fatalf("Server cert failed: %v", err)
	}
	es.Emit(store.OK, "pki", "", "Server cert — serial="+srv.Serial, "pki", nil)

	var certVault enrollment.CertVault
	vaultAddr := env("VAULT_ADDR", "")
	vaultToken := env("VAULT_TOKEN", "")
	if vaultAddr != "" && vaultToken != "" {
		vc := vault.NewClient(vaultAddr, vaultToken)
		if err := vc.Health(); err != nil {
			es.Emit(store.WARN, "vault", "", "Vault not reachable: "+err.Error(), "boot", nil)
		} else {
			certVault = vc
			es.Emit(store.OK, "vault", "", "Vault integration enabled", "boot", map[string]string{"addr": vaultAddr})
		}
	} else {
		es.Emit(store.INFO, "vault", "", "Vault integration disabled (VAULT_ADDR/VAULT_TOKEN not set)", "boot", nil)
	}

	enrollSvc := enrollment.NewService(pm, es, certVault)
	es.Emit(store.OK, "server", "", "Enrollment service ready", "boot", nil)

	apiSrv := api.NewServer(es, pm, enrollSvc)
	if envBool("LOCAL_SIMULATOR_ENABLED", false) {
		sim := simulator.NewLocal(simulator.LocalConfig{
			DockerCommand: env("LOCAL_SIMULATOR_DOCKER_CMD", "docker"),
			Image:         env("LOCAL_SIMULATOR_IMAGE", "edgeflux-edgeflux-agent:latest"),
			ServerURL:     env("LOCAL_SIMULATOR_SERVER_URL", "http://host.docker.internal:8080"),
			Network:       env("LOCAL_SIMULATOR_NETWORK", ""),
			NamePrefix:    env("LOCAL_SIMULATOR_NAME_PREFIX", "edgeflux-sim-"),
		})
		autoCreate := envBool("LOCAL_SIMULATOR_AUTO_CREATE", false)
		apiSrv.SetSimulator(sim, autoCreate)
		es.Emit(store.OK, "simulator", "", "Local simulator enabled", "boot", map[string]any{"auto_create": autoCreate})
	}
	es.Emit(store.OK, "server", "", "Platform ready on "+addr, "boot", nil)

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		log.Println("Shutting down...")
		os.Exit(0)
	}()

	if err := apiSrv.Start(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return n
}

func envBool(k string, d bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return d
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
