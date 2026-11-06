package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// DashboardAddress pins one network bind. DNS is used at installation, never while handling
// a browser request (DASHBOARD-LAN-624 in ADR 001, Decisions 2 and 4).
type DashboardAddress struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

func (a DashboardAddress) Address() string { return net.JoinHostPort(a.Host, strconv.Itoa(a.Port)) }
func (a DashboardAddress) Bind() string    { return net.JoinHostPort(a.IP, strconv.Itoa(a.Port)) }

func dashboardIP(ip net.IP) bool {
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast()
}

func dashboardName(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (a DashboardAddress) valid() bool {
	ip := net.ParseIP(a.IP)
	return dashboardIP(ip) && a.IP == ip.String() && ValidPort(a.Port) &&
		(a.Host == a.IP || dashboardName(a.Host))
}

// ResolveDashboardAddress accepts an address of this host, including a fully qualified DNS
// name that resolves to it. Multiple local answers choose the first canonical IP in sorted
// order; subsequent starts use the saved IP, without silently moving the listener.
func ResolveDashboardAddress(ctx context.Context, address string) (DashboardAddress, error) {
	return resolveDashboardAddress(ctx, address, net.InterfaceAddrs, net.DefaultResolver.LookupIPAddr)
}

func resolveDashboardAddress(ctx context.Context, address string, interfaces func() ([]net.Addr, error), lookup func(context.Context, string) ([]net.IPAddr, error)) (DashboardAddress, error) {
	invalid := Refusal{"dashboard_address_invalid", "use a non-loopback IP of this host or an FQDN that resolves to it, with a port from 1 to 65535"}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return DashboardAddress{}, invalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || !ValidPort(n) {
		return DashboardAddress{}, invalid
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	var candidates []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		if !dashboardIP(ip) {
			return DashboardAddress{}, invalid
		}
		host, candidates = ip.String(), []net.IPAddr{{IP: ip}}
	} else {
		if !dashboardName(host) {
			return DashboardAddress{}, invalid
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		candidates, err = lookup(ctx, host)
		if err != nil {
			return DashboardAddress{}, Refusal{"dashboard_address_unavailable", "the dashboard name could not be resolved: " + err.Error()}
		}
	}
	local, err := interfaces()
	if err != nil {
		return DashboardAddress{}, err
	}
	var matches []string
	for _, candidate := range candidates {
		if !dashboardIP(candidate.IP) {
			continue
		}
		for _, address := range local {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && candidate.IP.Equal(ip) {
				matches = append(matches, candidate.IP.String())
				break
			}
		}
	}
	if len(matches) == 0 {
		return DashboardAddress{}, Refusal{"dashboard_address_unavailable", "the dashboard address is not an address of this host"}
	}
	slices.Sort(matches)
	return DashboardAddress{Host: host, IP: matches[0], Port: n}, nil
}

// ReadDashboardAddress reads only an owner-checked private file. An absent file means the
// optional listener is off; malformed or unsafe files are reported, rather than enabled.
func ReadDashboardAddress(root string) (*DashboardAddress, error) {
	f, err := platform.OpenPrivate(filepath.Join(root, "dashboard-address.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dashboard_configuration_invalid: %w", err)
	}
	defer f.Close()
	var a DashboardAddress
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF || !a.valid() {
		return nil, errors.New("dashboard_configuration_invalid: invalid dashboard address file")
	}
	return &a, nil
}

// WriteDashboardAddress records the explicit installation choice. Nil removes it. The
// installer resolves names first; no credential is kept in this file.
func WriteDashboardAddress(root string, a *DashboardAddress) error {
	if a != nil && !a.valid() {
		return errors.New("dashboard_address_invalid: invalid dashboard address")
	}
	root, err := platform.PrivateDir(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "dashboard-address.json")
	// An explicit new choice can repair malformed content, but never an unsafe inode.
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if a == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return platform.SyncDir(root)
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return platform.WriteAtomic(path, append(data, '\n'), 0600)
}

// DashboardNetworkStatus describes the optional listener without exposing the private key.
type DashboardNetworkStatus struct {
	Address     string `json:"address,omitempty"`
	Bind        string `json:"bind,omitempty"`
	URL         string `json:"url,omitempty"`
	Fingerprint string `json:"sha256_fingerprint,omitempty"`
	Code        string `json:"code"`
	Error       string `json:"error,omitempty"`
	Step        string `json:"step,omitempty"`
}

func dashboardCertificate(root string, a DashboardAddress) (tls.Certificate, string, error) {
	path := filepath.Join(root, "dashboard-tls.pem")
	data := []byte(nil)
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		if err != nil || len(data) > 65536 {
			return tls.Certificate{}, "", errors.New("dashboard_certificate_invalid: unreadable certificate")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", fmt.Errorf("dashboard_certificate_invalid: %w", err)
	}
	var pair tls.Certificate
	if len(data) != 0 {
		pair, err = tls.X509KeyPair(data, data)
		if err != nil {
			return pair, "", fmt.Errorf("dashboard_certificate_invalid: %w", err)
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return pair, "", err
		}
		if cert.VerifyHostname(a.IP) != nil || cert.VerifyHostname(a.Host) != nil || time.Now().After(cert.NotAfter) {
			data = nil // an explicit new bind or an expired certificate needs a new identity
		}
	}
	if len(data) == 0 {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return pair, "", err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return pair, "", err
		}
		now := time.Now()
		cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Koinon dashboard"},
			NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(2, 0, 0),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			IPAddresses: []net.IP{net.ParseIP(a.IP)}}
		if a.Host != a.IP {
			cert.DNSNames = []string{a.Host}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if err != nil {
			return pair, "", err
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return pair, "", err
		}
		data = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})...)
		if err := platform.WriteAtomic(path, data, 0600); err != nil {
			return pair, "", err
		}
		pair, err = tls.X509KeyPair(data, data)
		if err != nil {
			return pair, "", err
		}
	}
	hash := sha256.Sum256(pair.Certificate[0])
	return pair, strings.ToUpper(hex.EncodeToString(hash[:])), nil
}

// networkDashboardHandler has no agent routes. Its listener identity is fixed at startup,
// and the common dashboard keeps the existing secret backoff, sessions and CSRF checks.
func (d *Daemon) networkDashboardHandler(a DashboardAddress) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardHeaders(w)
		if !strings.HasPrefix(r.URL.Path, "/dashboard/") {
			http.NotFound(w, r)
			return
		}
		authority := dashboardHTTPSAuthority(r.Host)
		if r.TLS == nil || authority != dashboardHTTPSAuthority(a.Address()) && authority != dashboardHTTPSAuthority(a.Bind()) {
			http.Error(w, "foreign host", http.StatusForbidden)
			return
		}
		set, err := d.store.maintainerSecretSet(r.Context())
		if err != nil || !set {
			http.Error(w, "dashboard_secret_required", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/dashboard/login" && r.URL.Query().Has("token") {
			http.Error(w, "network login requires the maintainer secret", http.StatusForbidden)
			return
		}
		d.dashboard.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), networkDashboardKey{}, true)))
	})
}

type networkDashboardKey struct{}

// RFC 9110 sections 4.2.3 and 4.3.1: HTTPS authorities and origins normalize host case and
// the default port. Browsers omit :443 even when the installation explicitly chose it.
func dashboardHTTPSAuthority(authority string) string {
	authority = strings.ToLower(authority)
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return authority
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return authority
	}
	if n == 443 {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(n))
}

func networkDashboard(r *http.Request) bool {
	value, _ := r.Context().Value(networkDashboardKey{}).(bool)
	return value
}

func (d *Daemon) startDashboardNetwork(interfaces func() ([]net.Addr, error), listen func(string, string) (net.Listener, error)) {
	a, err := ReadDashboardAddress(d.root)
	if a == nil && err == nil {
		return
	}
	d.networkAddress = a
	d.networkInterfaces = interfaces
	d.networkStatus = &DashboardNetworkStatus{Code: "dashboard_configuration_invalid", Step: "Run koinon install --dashboard-address HOST:PORT to configure the network dashboard."}
	if a != nil {
		d.networkStatus.Address, d.networkStatus.Bind = a.Address(), a.Bind()
		d.networkStatus.URL = "https://" + a.Address() + "/dashboard/"
	}
	failed := func(code string, err error) {
		d.networkMu.Lock()
		d.networkStatus.Code = code
		d.networkStatus.Error = err.Error()
		d.networkStatus.Step = "Run koinon install to retry the configured network dashboard."
		if code == "dashboard_configuration_invalid" {
			d.networkStatus.Step = "Run koinon install --dashboard-address HOST:PORT to repair the network setting."
		}
		if code == "dashboard_certificate_invalid" {
			d.networkStatus.Step = "Check dashboard-tls.pem in the private state directory, then run koinon install to retry."
		}
		d.networkMu.Unlock()
		if d.logEvent != nil {
			d.logEvent("dashboard_network_unavailable", map[string]any{"address": d.networkStatus.Bind, "code": code, "error": err.Error()})
		}
	}
	if err != nil {
		failed("dashboard_configuration_invalid", err)
		return
	}
	set, err := d.store.maintainerSecretSet(context.Background())
	if err != nil {
		failed("dashboard_secret_unavailable", err)
		return
	}
	if !set {
		failed("dashboard_secret_required", ErrSecretUnset)
		return
	}
	if _, err := resolveDashboardAddress(context.Background(), a.Bind(), interfaces, nil); err != nil {
		failed("dashboard_address_unavailable", err)
		return
	}
	cert, fingerprint, err := dashboardCertificate(d.root, *a)
	if err != nil {
		failed("dashboard_certificate_invalid", err)
		return
	}
	d.networkStatus.Fingerprint = fingerprint
	l, err := listen("tcp", a.Bind())
	if err != nil {
		failed("dashboard_address_unavailable", err)
		return
	}
	d.networkListener = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	server := &http.Server{Handler: d.networkDashboardHandler(*a), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	d.networkServer = server
	d.servers = append(d.servers, server)
	d.networkStatus.Code, d.networkStatus.Step = "running", ""
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if err := server.Serve(d.networkListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed("dashboard_address_unavailable", err)
		}
	}()
}

func (d *Daemon) dashboardNetworkStatus(ctx context.Context) *DashboardNetworkStatus {
	d.networkMu.RLock()
	if d.networkStatus == nil {
		d.networkMu.RUnlock()
		return nil
	}
	status := *d.networkStatus
	address, interfaces, server := d.networkAddress, d.networkInterfaces, d.networkServer
	d.networkMu.RUnlock()
	if address == nil {
		return &status
	}
	// A bind that worked at startup can lose its interface after a DHCP change. A status
	// read observes that loss, closes only this listener and records the actual error once.
	if status.Code == "running" {
		if _, err := resolveDashboardAddress(ctx, address.Bind(), interfaces, nil); err != nil {
			changed := false
			d.networkMu.Lock()
			if d.networkStatus.Code == "running" {
				d.networkStatus.Code, d.networkStatus.Error = "dashboard_address_unavailable", err.Error()
				d.networkStatus.Step = "Run koinon install to resolve the dashboard address again and retry."
				changed = true
			}
			status = *d.networkStatus
			d.networkMu.Unlock()
			if changed {
				if server != nil {
					server.Close()
				}
				if d.logEvent != nil {
					d.logEvent("dashboard_network_unavailable", map[string]any{"address": address.Bind(), "code": status.Code, "error": err.Error()})
				}
			}
		}
	}
	set, err := d.store.maintainerSecretSet(ctx)
	if err != nil || !set {
		status.Code, status.Step = "dashboard_secret_required", "Set the maintainer secret with koinon maintainer secret set or generate, then run koinon install to start the network dashboard."
		status.Error = ErrSecretUnset.Error()
	} else if status.Code == "dashboard_secret_required" {
		status.Code, status.Step = "dashboard_restart_required", "Run koinon install to start the network dashboard with the secret that is now set."
		status.Error = ""
	}
	return &status
}
