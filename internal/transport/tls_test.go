package transport_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/transport"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := transport.GenerateSelfSignedCert(nil)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert(nil) error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("empty Certificate slice")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate error: %v", err)
	}

	if _, ok := cert.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Errorf("PrivateKey is %T, want *ecdsa.PrivateKey", cert.PrivateKey)
	}

	if x509Cert.Subject.CommonName != "wideboi" {
		t.Errorf("CommonName = %q, want wideboi", x509Cert.Subject.CommonName)
	}

	if time.Until(x509Cert.NotAfter) < 300*24*time.Hour {
		t.Errorf("NotAfter is too soon: %v", x509Cert.NotAfter)
	}

	// Must contain loopback IP and DNS
	has127 := false
	hasV6 := false
	for _, ip := range x509Cert.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			has127 = true
		}
		if ip.Equal(net.ParseIP("::1")) {
			hasV6 = true
		}
	}
	if !has127 {
		t.Error("IPAddresses missing 127.0.0.1")
	}
	if !hasV6 {
		t.Error("IPAddresses missing ::1")
	}

	hasLocalhost := false
	for _, dns := range x509Cert.DNSNames {
		if dns == "localhost" {
			hasLocalhost = true
		}
	}
	if !hasLocalhost {
		t.Error("DNSNames missing localhost")
	}
}

func TestGenerateSelfSignedCertWithHosts(t *testing.T) {
	hosts := []string{"192.168.1.50", "wideboi.lan", "10.0.0.1:8080"}
	cert, err := transport.GenerateSelfSignedCert(hosts)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate error: %v", err)
	}

	hasLANIP := false
	has10IP := false
	for _, ip := range x509Cert.IPAddresses {
		if ip.Equal(net.ParseIP("192.168.1.50")) {
			hasLANIP = true
		}
		if ip.Equal(net.ParseIP("10.0.0.1")) {
			has10IP = true
		}
	}
	if !hasLANIP {
		t.Error("IPAddresses missing 192.168.1.50")
	}
	if !has10IP {
		t.Error("IPAddresses missing 10.0.0.1 from host:port")
	}

	hasDomain := false
	for _, dns := range x509Cert.DNSNames {
		if dns == "wideboi.lan" {
			hasDomain = true
		}
	}
	if !hasDomain {
		t.Error("DNSNames missing wideboi.lan")
	}
}

func TestGenerateSelfSignedCertWildcardPort(t *testing.T) {
	cert, err := transport.GenerateSelfSignedCert([]string{":8080"})
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert(:8080) error: %v", err)
	}
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate error: %v", err)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("net.InterfaceAddrs error: %v", err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			found := false
			for _, ip := range x509Cert.IPAddresses {
				if ip.Equal(ipnet.IP) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("IPAddresses missing interface IP %v for wildcard :8080", ipnet.IP)
			}
		}
	}
}

func TestLoadOrGenerateTLSConfig(t *testing.T) {
	// 1. Ephemeral self-signed config
	cfg, err := transport.LoadOrGenerateTLSConfig("", "", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("LoadOrGenerateTLSConfig empty paths error: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(cfg.Certificates))
	}

	// 2. Load from PEM files
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	// Generate and save a test keypair
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	certTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}

	loadedCfg, err := transport.LoadOrGenerateTLSConfig(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("LoadOrGenerateTLSConfig with files error: %v", err)
	}
	if len(loadedCfg.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(loadedCfg.Certificates))
	}

	// 3. Error on missing file
	if _, err := transport.LoadOrGenerateTLSConfig(filepath.Join(dir, "nonexistent.pem"), keyFile, ""); err == nil {
		t.Error("LoadOrGenerateTLSConfig with missing file succeeded, want error")
	}
}
