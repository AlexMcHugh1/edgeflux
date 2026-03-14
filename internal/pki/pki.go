package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type CertBundle struct {
	Cert       *x509.Certificate
	Key        *ecdsa.PrivateKey
	CertPEM    []byte
	KeyPEM     []byte
	Serial     string
	Thumbprint string
}

type Manager struct {
	CertsDir      string
	RootCA        *CertBundle
	IntCA         *CertBundle
	DeviceCertTTL time.Duration
}

func NewManager(certsDir string) *Manager {
	os.MkdirAll(certsDir, 0700)
	return &Manager{CertsDir: certsDir, DeviceCertTTL: 10 * time.Minute}
}

func (m *Manager) SetDeviceCertTTL(ttl time.Duration) {
	if ttl <= 0 {
		m.DeviceCertTTL = 10 * time.Minute
		return
	}
	m.DeviceCertTTL = ttl
}

func serialNumber() *big.Int {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, _ := rand.Int(rand.Reader, max)
	return n
}

func thumbprint(cert *x509.Certificate) string {
	h := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(h[:])
}

func writePEM(path string, block *pem.Block, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(block), mode)
}

func (m *Manager) BootstrapRootCA() (*CertBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("gen root key: %w", err)
	}
	serial := serialNumber()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "EdgeFlux Root CA", Organization: []string{"EdgeFlux Systems"}, Country: []string{"GB"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	b := &CertBundle{Cert: cert, Key: key, CertPEM: certPEM, KeyPEM: keyPEM, Serial: serial.Text(16), Thumbprint: thumbprint(cert)}
	m.RootCA = b
	os.WriteFile(filepath.Join(m.CertsDir, "root-ca.pem"), certPEM, 0644)
	os.WriteFile(filepath.Join(m.CertsDir, "root-ca-key.pem"), keyPEM, 0600)
	return b, nil
}

func (m *Manager) BootstrapIntermediateCA() (*CertBundle, error) {
	if m.RootCA == nil {
		return nil, fmt.Errorf("root CA not initialized")
	}
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial := serialNumber()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "EdgeFlux Enrollment CA", Organization: []string{"EdgeFlux Systems"}, OrganizationalUnit: []string{"Device Provisioning"}, Country: []string{"GB"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.RootCA.Cert, &key.PublicKey, m.RootCA.Key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	chain := append(certPEM, m.RootCA.CertPEM...)

	b := &CertBundle{Cert: cert, Key: key, CertPEM: certPEM, KeyPEM: keyPEM, Serial: serial.Text(16), Thumbprint: thumbprint(cert)}
	m.IntCA = b
	os.WriteFile(filepath.Join(m.CertsDir, "intermediate-ca.pem"), certPEM, 0644)
	os.WriteFile(filepath.Join(m.CertsDir, "intermediate-ca-key.pem"), keyPEM, 0600)
	os.WriteFile(filepath.Join(m.CertsDir, "ca-chain.pem"), chain, 0644)
	return b, nil
}

func (m *Manager) SignCSR(csrPEM []byte, deviceID string) (*CertBundle, error) {
	if m.IntCA == nil {
		return nil, fmt.Errorf("intermediate CA required")
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature invalid: %w", err)
	}
	serial := serialNumber()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: deviceID + ".edge.edgeflux.local", Organization: []string{"EdgeFlux Systems"}, OrganizationalUnit: []string{"Devices"}},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(m.DeviceCertTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{deviceID + ".edge.edgeflux.local"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.IntCA.Cert, csr.PublicKey, m.IntCA.Key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	b := &CertBundle{Cert: cert, CertPEM: certPEM, Serial: serial.Text(16), Thumbprint: thumbprint(cert)}

	devDir := filepath.Join(m.CertsDir, "devices", deviceID)
	os.MkdirAll(devDir, 0700)
	os.WriteFile(filepath.Join(devDir, "cert.pem"), certPEM, 0644)
	return b, nil
}

func (m *Manager) GenerateServerCert(hosts []string) (*CertBundle, error) {
	if m.IntCA == nil {
		return nil, fmt.Errorf("intermediate CA required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial := serialNumber()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"EdgeFlux Systems"}},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.IntCA.Cert, &key.PublicKey, m.IntCA.Key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	chain := append(certPEM, m.IntCA.CertPEM...)

	b := &CertBundle{Cert: cert, Key: key, CertPEM: chain, KeyPEM: keyPEM, Serial: serial.Text(16), Thumbprint: thumbprint(cert)}
	os.WriteFile(filepath.Join(m.CertsDir, "server.pem"), chain, 0644)
	os.WriteFile(filepath.Join(m.CertsDir, "server-key.pem"), keyPEM, 0600)
	return b, nil
}

func (m *Manager) GenerateDeviceCert(deviceID string) (*CertBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csrTmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: deviceID + ".edge.edgeflux.local", Organization: []string{"EdgeFlux Systems"}},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTmpl, key)
	if err != nil {
		return nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	b, err := m.SignCSR(csrPEM, deviceID)
	if err != nil {
		return nil, err
	}
	b.Key = key
	keyDER, _ := x509.MarshalECPrivateKey(key)
	b.KeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return b, nil
}
