// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stpinkie/rhizome/pkg/logger"
)

// Track 110 — ACP-over-HTTPS: an optional TLS listener that upgrades
// /rhizome/acp to a websocket and adapts it to the same byte-stream the
// libp2p bridge splices — ndjson ACP frames flow unchanged, so the
// identical session_open → gate → session/new machinery serves buyers
// that have no Rhizome mesh identity. TLS is mandatory; the advertised
// tls_fingerprint pins the leaf cert for buyers (TOFU).

const (
	// httpsHelloTimeout bounds upgrade-to-first-message; beyond that the
	// stream carries its own session/peer cadence.
	httpsHelloTimeout = 15 * time.Second
	httpsDialTimeout  = 20 * time.Second
	// wsReadBuf bounds one websocket message — ACP ndjson frames are
	// small; the cap rejects pathological frames before buffering.
	wsMsgMaxBytes = 4 << 20

	httpsCertFile = "https-cert.pem"
	httpsKeyFile  = "https-key.pem"
)

// wsConn adapts a websocket connection to io.ReadWriteCloser: binary
// messages carry stream bytes, so ndjson ACP flows exactly as it does
// over the libp2p splice (one Write call = one message; Read drains
// message payloads transparently). Ping/pong/close frames are handled by
// the gorilla read pump.
type wsConn struct {
	c *websocket.Conn
	r io.Reader // reader for the in-flight message, nil between messages

	writeMu   sync.Mutex
	firstRead sync.Once // clears the upgrade-to-first-message deadline
	clearRead bool      // set when the server armed a read deadline
	closed    chan struct{}
	close     sync.Once
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.r == nil {
			mt, r, err := c.c.NextReader()
			if err != nil {
				return 0, io.EOF
			}
			if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
				continue // control/other frames: handled by the read pump
			}
			c.r = io.LimitReader(r, wsMsgMaxBytes)
		}
		if c.clearRead {
			c.firstRead.Do(func() { _ = c.c.SetReadDeadline(time.Time{}) })
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.c.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	c.close.Do(func() {
		close(c.closed)
		_ = c.c.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		_ = c.c.Close()
	})
	return nil
}

// httpsServer is the TLS+websocket listener for non-mesh buyers. Each
// upgraded conn becomes a marketAgent bound to "https:<remote-ip>" — the
// peer label for rate limiting and audit — then drives the shared
// serveACP path. There is no token hello: TLS + the escrow gate are the
// authentication.
type httpsServer struct {
	ln          net.Listener
	fingerprint string // sha256 of the leaf cert DER — what adverts pin
	advertise   string // serve_https_advertise at startup (restart to change)
	mgr         *sessionMgr
	audit       *auditLogger
	connID      func() uint64
	sem         chan struct{}
	wg          sync.WaitGroup
	srv         *http.Server
	closed      chan struct{}
	once        sync.Once
}

// startHTTPS binds the configured listen address (TLS mandatory),
// loading or generating the serving cert and returning the fingerprint
// the advert publishes.
func startHTTPS(
	mc *marketConfig, moduleDir string, mgr *sessionMgr,
	connID func() uint64, audit *auditLogger,
) (*httpsServer, error) {
	cert, fp, err := loadOrGenHTTPSCert(moduleDir, mc.httpsCert, mc.httpsKey)
	if err != nil {
		return nil, err
	}
	ln, err := tls.Listen("tcp", mc.httpsListen, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("https listen %s: %w", mc.httpsListen, err)
	}
	maxConns := mc.httpsMaxConns
	if maxConns < 1 {
		maxConns = 64
	}
	h := &httpsServer{
		ln:          ln,
		fingerprint: fp,
		advertise:   mc.httpsAdvertise,
		mgr:         mgr,
		audit:       audit,
		connID:      connID,
		sem:         make(chan struct{}, maxConns),
		closed:      make(chan struct{}),
	}
	up := websocket.Upgrader{
		// The gate is the auth boundary — Origin is a browser concept; the
		// escrow presentation + VerifyLock are what bind work to payment.
		CheckOrigin:     func(*http.Request) bool { return true },
		ReadBufferSize:  16 << 10,
		WriteBufferSize: 16 << 10,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rhizome/acp", func(w http.ResponseWriter, r *http.Request) {
		h.serveWS(w, r, &up)
	})
	h.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		if err := h.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			audit.log("market.https.error", map[string]any{"error": err.Error()})
		}
	}()
	return h, nil
}

// serveWS upgrades one request and hands the adapted conn to the ACP
// agent machinery — identical post-upgrade presentation to the libp2p
// path (session_open → VerifyLock gate → splice).
func (h *httpsServer) serveWS(w http.ResponseWriter, r *http.Request, up *websocket.Upgrader) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		h.audit.log("market.stream.rejected", map[string]any{
			"peer": r.RemoteAddr, "reason": "https conn cap",
		})
		http.Error(w, "conn cap", http.StatusServiceUnavailable)
		return
	}
	h.wg.Add(1)
	defer h.wg.Done()

	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // upgrade already wrote the refusal
	}
	// Bound upgrade → first message (the buyer's initialize/session_open);
	// cleared once the first data message arrives — session cadence is
	// the stream's own thereafter.
	_ = ws.SetReadDeadline(time.Now().Add(httpsHelloTimeout))
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	peer := "https:" + host
	h.audit.log("market.stream.accept", map[string]any{
		"peer": peer, "protocol": acpMarketProtocol, "transport": "wss",
	})
	logger.InfoCF("market", "https stream accepted", map[string]any{"peer": peer})
	conn := &wsConn{c: ws, clearRead: true, closed: make(chan struct{})}
	agent := newConnAgent(peer, h.connID(), h.mgr, h.audit)
	serveAgentConn(conn, agent, h.mgr)
}

// serveAgentConn runs a verified conn (any transport) through the ACP
// agent side — extracted from bridgeServer so HTTPS and libp2p share one
// path. Blocks until the transport closes, then finalizes the conn's
// sessions.
func serveAgentConn(rwc io.ReadWriteCloser, a *marketAgent, mgr *sessionMgr) {
	defer func() { _ = rwc.Close() }()
	agentConn := newAgentConn(a, rwc, rwc)
	a.attachUpstream(agentConn)
	<-agentConn.Done()
	a.onConnClose()
	if mgr != nil {
		mgr.finalizeConn(a.connID)
	}
}

// Close stops the listener and drains in-flight conns briefly.
func (h *httpsServer) Close() {
	h.once.Do(func() {
		close(h.closed)
		_ = h.srv.Close()
		_ = h.ln.Close()
	})
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// loadOrGenHTTPSCert resolves the serving certificate: the configured
// cert/key pair when both fields are set, else a persisted self-signed
// pair under the module dir (generated once — a stable tls_fingerprint
// survives restarts, which is what makes TOFU workable).
func loadOrGenHTTPSCert(
	moduleDir, certPath, keyPath string,
) (tls.Certificate, string, error) {
	if certPath != "" && keyPath != "" {
		return loadHTTPSCert(certPath, keyPath)
	}
	if certPath != "" || keyPath != "" {
		return tls.Certificate{}, "", fmt.Errorf(
			"serve_https_cert and serve_https_key must be set together (or neither for an auto self-signed pair)",
		)
	}
	certPath = filepath.Join(moduleDir, httpsCertFile)
	keyPath = filepath.Join(moduleDir, httpsKeyFile)
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return loadHTTPSCert(certPath, keyPath)
		}
	}
	if err := genSelfSignedCert(certPath, keyPath); err != nil {
		return tls.Certificate{}, "", err
	}
	return loadHTTPSCert(certPath, keyPath)
}

func loadHTTPSCert(certPath, keyPath string) (tls.Certificate, string, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("https cert/key load: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("https cert parse: %w", err)
	}
	fp := sha256.Sum256(leaf.Raw)
	return cert, hex.EncodeToString(fp[:]), nil
}

// genSelfSignedCert writes a fresh ECDSA P-256 self-signed cert+key pair —
// IP SANs are what matter for TOFU endpoints (buyers pin the leaf sha256,
// not a hostname chain).
func genSelfSignedCert(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "rhizome-market"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(
		&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
}

// advertiseEndpoint renders the advert's wss endpoint for the *running*
// listener: serve_https_advertise wins (public DNS/NAT cases); a concrete
// bound listen host advertises itself; a wildcard listen without an
// advertise host emits nothing (there is no honest guess for "0.0.0.0").
// The bound addr is used — config edits to serve_https_* take effect on
// restart, so the advert never claims an endpoint the listener isn't on.
func (h *httpsServer) advertiseEndpoint() string {
	host := h.advertise
	if host == "" {
		listenHost, port, err := net.SplitHostPort(h.ln.Addr().String())
		if err != nil || listenHost == "" {
			return ""
		}
		if ip := net.ParseIP(listenHost); ip != nil && ip.IsUnspecified() {
			return ""
		}
		host = net.JoinHostPort(listenHost, port)
	}
	if strings.HasPrefix(host, "wss://") {
		return strings.TrimSuffix(host, "/") + "/rhizome/acp"
	}
	if strings.HasPrefix(host, "https://") {
		return "wss://" + strings.TrimPrefix(
			strings.TrimSuffix(host, "/"),
			"https://",
		) + "/rhizome/acp"
	}
	return "wss://" + host + "/rhizome/acp"
}

// dialWSS dials an advert-advertised https endpoint: TLS mandatory, and
// the leaf cert's sha256 must equal the fingerprint the advert pinned —
// TOFU without any CA dependency.
func dialWSS(endpoint, fingerprint string) (io.ReadWriteCloser, error) {
	u := endpoint
	if strings.HasPrefix(u, "https://") {
		u = "wss://" + strings.TrimPrefix(u, "https://")
	}
	if !strings.HasPrefix(u, "wss://") {
		return nil, fmt.Errorf("https endpoint %q is not wss://", endpoint)
	}
	fp := strings.ToLower(strings.TrimSpace(fingerprint))
	if fp == "" {
		return nil, fmt.Errorf("advert carries no tls_fingerprint — refusing unpinned dial")
	}
	d := websocket.Dialer{
		HandshakeTimeout: httpsDialTimeout,
		TLSClientConfig: &tls.Config{
			// TOFU: the advert's fingerprint is the trust anchor — CA
			// verification is bypassed but the leaf pin is verified
			// byte-exact below, so a substituted cert never connects.
			InsecureSkipVerify: true, //nolint:gosec // pinned by fingerprint below
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return fmt.Errorf("server presented no certificate")
				}
				sum := sha256.Sum256(rawCerts[0])
				if got := hex.EncodeToString(sum[:]); got != fp {
					return fmt.Errorf(
						"tls fingerprint mismatch: got %s, advert pinned %s", got, fp)
				}
				return nil
			},
		},
	}
	c, resp, err := d.Dial(u, nil)
	if err != nil {
		if resp != nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			return nil, fmt.Errorf(
				"wss dial %s: %v (http %d)", u, err, code)
		}
		return nil, fmt.Errorf("wss dial %s: %w", u, err)
	}
	return &wsConn{c: c, closed: make(chan struct{})}, nil
}
