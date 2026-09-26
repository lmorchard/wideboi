package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateSelfSignedCert generates an in-memory ephemeral self-signed ECDSA P-256
// certificate valid for one year. SANs include localhost, 127.0.0.1, ::1, and any
// hostnames or IPs provided in hosts. If any host is empty, "0.0.0.0", or "::",
// all unicast addresses of local network interfaces are added to the SANs.
func GenerateSelfSignedCert(hosts []string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate ecdsa key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "wideboi",
			Organization: []string{"wideboi"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	ipMap := map[string]net.IP{
		"127.0.0.1": net.ParseIP("127.0.0.1"),
		"::1":       net.ParseIP("::1"),
	}
	dnsMap := map[string]bool{
		"localhost": true,
	}

	addHost := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" {
			return
		}
		// Strip brackets for IPv6 literals
		if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			h = h[1 : len(h)-1]
		}
		// If host contains port (e.g. 192.168.1.50:8080 or :8080)
		if hostPart, _, err := net.SplitHostPort(h); err == nil {
			h = hostPart
		}
		if h == "" {
			h = "0.0.0.0"
		}

		if ip := net.ParseIP(h); ip != nil {
			if ip.IsUnspecified() {
				// 0.0.0.0 or :: - enumerate local interfaces
				if addrs, err := net.InterfaceAddrs(); err == nil {
					for _, a := range addrs {
						if ipnet, ok := a.(*net.IPNet); ok {
							ipMap[ipnet.IP.String()] = ipnet.IP
						}
					}
				}
			} else {
				ipMap[ip.String()] = ip
			}
		} else {
			dnsMap[strings.ToLower(h)] = true
		}
	}

	for _, h := range hosts {
		addHost(h)
	}

	for _, ip := range ipMap {
		template.IPAddresses = append(template.IPAddresses, ip)
	}
	for name := range dnsMap {
		template.DNSNames = append(template.DNSNames, name)
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}, nil
}

// LoadOrGenerateTLSConfig loads a TLS certificate and private key from file paths
// if provided, or generates an ephemeral self-signed certificate if certFile and keyFile
// are empty. wsAddr is parsed to include the server host in the certificate's SANs.
func LoadOrGenerateTLSConfig(certFile, keyFile, wsAddr string) (*tls.Config, error) {
	var cert tls.Certificate
	if certFile != "" && keyFile != "" {
		var err error
		cert, err = tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load tls keypair: %w", err)
		}
	} else if certFile != "" || keyFile != "" {
		return nil, fmt.Errorf("both certFile and keyFile must be specified")
	} else {
		var hosts []string
		if wsAddr != "" {
			hosts = append(hosts, wsAddr)
		}
		var err error
		cert, err = GenerateSelfSignedCert(hosts)
		if err != nil {
			return nil, fmt.Errorf("generate self-signed cert: %w", err)
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
