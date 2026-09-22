package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/web3"
)

func testWeb3Config(signing bool) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Tools.Web3.Enabled = true
	cfg.Tools.Web3.Signing.Enabled = signing
	return cfg
}

// web3Home builds a temp wallet home with one key.
func web3Home(t *testing.T) string {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "test-passphrase")
	home := t.TempDir()
	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{Enabled: true})
	if err != nil {
		t.Fatalf("open stack: %v", err)
	}
	if _, err := stack.Wallets.Generate("test"); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return home
}

func TestWeb3HandlerRequiresAuth(t *testing.T) {
	h := newWeb3Handler("secret-token", testWeb3Config(true), t.TempDir(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/web3/pending", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWeb3HandlerDisabledGate(t *testing.T) {
	cfg := testWeb3Config(true)
	cfg.Tools.Web3.Enabled = false
	h := newWeb3Handler(testTasksToken, cfg, t.TempDir(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/web3/pending", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestWeb3HandlerPendingAndWallet(t *testing.T) {
	home := web3Home(t)
	h := newWeb3Handler(testTasksToken, testWeb3Config(true), home, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/web3/pending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pending status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/web3/wallet", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("wallet status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var w struct {
		Addresses []web3.WalletEntry `json:"addresses"`
		Default   string             `json:"default"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Addresses) != 1 || w.Default == "" {
		t.Fatalf("wallet response = %s", rec.Body.String())
	}
	// Response must not contain ciphertext or key material.
	if strings.Contains(rec.Body.String(), "ciphertext") ||
		strings.Contains(rec.Body.String(), "nonce") {
		t.Fatalf("wallet response leaked key fields: %s", rec.Body.String())
	}
}

func TestWeb3HandlerRejectFlow(t *testing.T) {
	home := web3Home(t)
	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := stack.Wallets.Default()
	e, err := stack.Pending.Submit(&web3.PendingEntry{
		Kind: web3.KindSign, From: def, Message: "0xdead", Summary: "sign test",
	})
	if err != nil {
		t.Fatal(err)
	}

	bus := runtimeevents.NewBus()
	h := newWeb3Handler(testTasksToken, testWeb3Config(true), home, bus)

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/web3/approvals/"+e.ID,
		strings.NewReader(`{"action":"reject"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got, _ := stack.Pending.Get(e.ID)
	if got.Status != web3.StatusRejected || got.ResolvedBy != "daemon" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestWeb3HandlerApproveSignKind(t *testing.T) {
	home := web3Home(t)
	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := stack.Wallets.Default()
	e, err := stack.Pending.Submit(&web3.PendingEntry{
		Kind: web3.KindSign, From: def, Message: "0xdeadbeef", Summary: "sign test",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := newWeb3Handler(testTasksToken, testWeb3Config(true), home, nil)
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/web3/approvals/"+e.ID,
		strings.NewReader(`{"action":"approve"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var done web3.PendingEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &done); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if done.Status != web3.StatusDone || !strings.HasPrefix(done.Result, "0x") {
		t.Fatalf("entry = %+v", done)
	}
}

func TestWeb3HandlerApproveNeedsSigningGate(t *testing.T) {
	home := web3Home(t)
	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := stack.Wallets.Default()
	e, _ := stack.Pending.Submit(&web3.PendingEntry{
		Kind: web3.KindSign, From: def, Message: "0xdead", Summary: "x",
	})

	h := newWeb3Handler(testTasksToken, testWeb3Config(false), home, nil)
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/web3/approvals/"+e.ID,
		strings.NewReader(`{"action":"approve"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
