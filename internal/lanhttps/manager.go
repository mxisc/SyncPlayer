// Package lanhttps manages trusted certificates for private LAN endpoints.
package lanhttps

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	legolog "github.com/go-acme/lego/v4/log"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/registration"
	"golang.org/x/sys/windows"
)

type Settings struct {
	Domain       string                 `json:"domain"`
	ID           string                 `json:"id"`
	Token        string                 `json:"token"`
	AccountKey   []byte                 `json:"account_key"`
	Registration *registration.Resource `json:"registration"`
	Certificate  []byte                 `json:"certificate"`
	PrivateKey   []byte                 `json:"private_key"`
}

type Manager struct {
	mu       sync.Mutex
	dir      string
	settings Settings
	cert     atomic.Pointer[tls.Certificate]
}

type account struct {
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (a account) GetEmail() string                        { return "" }
func (a account) GetRegistration() *registration.Resource { return a.registration }
func (a account) GetPrivateKey() crypto.PrivateKey        { return a.key }

// Open returns os.ErrNotExist when HTTPS has not been configured.
func Open(dir string) (*Manager, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.dpapi"))
	if err != nil {
		return nil, err
	}
	plain, err := protect(data, false)
	if err != nil {
		return nil, err
	}
	m := &Manager{dir: dir}
	if json.Unmarshal(plain, &m.settings) != nil {
		return nil, errors.New("HTTPS 配置损坏")
	}
	if err := validate(m.settings); err != nil {
		return nil, err
	}
	return m, nil
}

func validate(s Settings) error {
	if s.Domain == "" || strings.ContainsAny(s.Domain, "/:\\ \t\r\n") || !strings.Contains(s.Domain, ".") || strings.HasPrefix(s.Domain, "*") {
		return errors.New("HTTPS 域名无效")
	}
	if s.ID == "" || s.Token == "" {
		return errors.New("DNSPod 凭据未配置")
	}
	return nil
}

// Configure preserves existing account and certificate data for the same domain.
func Configure(dir string, s Settings) (*Manager, error) {
	s.Domain = strings.ToLower(strings.TrimSpace(s.Domain))
	if err := validate(s); err != nil {
		return nil, err
	}
	m, err := Open(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if m == nil {
		m = &Manager{dir: dir}
	}
	if m.settings.Domain == s.Domain {
		m.settings.ID = s.ID
		m.settings.Token = s.Token
	} else {
		m.settings = s
	}
	return m, m.save()
}

func (m *Manager) Domain() string { return m.settings.Domain }

func (m *Manager) save() error {
	data, err := json.Marshal(m.settings)
	if err != nil {
		return err
	}
	data, err = protect(data, true)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(m.dir, "settings.dpapi")
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func protect(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("HTTPS 配置为空")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, errors.New("HTTPS 配置加解密失败")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}

func (m *Manager) loadCertificate() (*tls.Certificate, error) {
	c, err := tls.X509KeyPair(m.settings.Certificate, m.settings.PrivateKey)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(m.settings.Domain); err != nil {
		return nil, err
	}
	c.Leaf = leaf
	return &c, nil
}

// Ensure obtains or renews using DNS-01; no inbound Internet ports are needed.
func (m *Manager) Ensure() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, oldErr := m.loadCertificate()
	if oldErr == nil && time.Until(current.Leaf.NotAfter) > 0 {
		m.cert.Store(current)
	}
	if oldErr == nil && time.Until(current.Leaf.NotAfter) > 30*24*time.Hour {
		m.cert.Store(current)
		return nil
	}
	// Provider/library diagnostics are deliberately not sent to application logs.
	legolog.Logger = log.New(io.Discard, "", 0)
	if len(m.settings.AccountKey) == 0 {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return err
		}
		m.settings.AccountKey = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
		if err = m.save(); err != nil {
			return err
		}
	}
	block, _ := pem.Decode(m.settings.AccountKey)
	if block == nil {
		return errors.New("ACME 账户密钥损坏")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return errors.New("ACME 账户密钥无效")
	}
	user := &account{key: key, registration: m.settings.Registration}
	cfg := lego.NewConfig(user)
	cfg.HTTPClient.Timeout = 45 * time.Second
	client, err := lego.NewClient(cfg)
	if err != nil {
		return errors.New("无法创建 ACME 客户端")
	}
	dnsCfg := dnspod.NewDefaultConfig()
	dnsCfg.LoginToken = m.settings.ID + "," + m.settings.Token
	dnsCfg.PropagationTimeout = 5 * time.Minute
	provider, err := dnspod.NewDNSProviderConfig(dnsCfg)
	if err != nil {
		return errors.New("无法初始化 DNSPod 验证")
	}
	if err = client.Challenge.SetDNS01Provider(provider, dns01.AddRecursiveNameservers([]string{"1.1.1.1:53", "223.5.5.5:53"})); err != nil {
		return errors.New("无法配置 DNS 验证")
	}
	if m.settings.Registration == nil {
		m.settings.Registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		user.registration = m.settings.Registration
		if err != nil {
			return errors.New("ACME 账户注册失败")
		}
		if err = m.save(); err != nil {
			return err
		}
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{m.settings.Domain}, Bundle: true})
	if err != nil {
		if oldErr == nil && time.Until(current.Leaf.NotAfter) > 0 {
			m.cert.Store(current)
		}
		message := strings.ReplaceAll(err.Error(), m.settings.Token, "[REDACTED]")
		message = regexp.MustCompile(`https?://\S+`).ReplaceAllString(message, "[URL]")
		return fmt.Errorf("证书签发或续期失败：%s", message)
	}
	oldCert, oldKey := m.settings.Certificate, m.settings.PrivateKey
	m.settings.Certificate = resource.Certificate
	m.settings.PrivateKey = resource.PrivateKey
	c, err := m.loadCertificate()
	if err != nil {
		m.settings.Certificate = oldCert
		m.settings.PrivateKey = oldKey
		return errors.New("签发证书校验失败")
	}
	if err = m.save(); err != nil {
		m.settings.Certificate = oldCert
		m.settings.PrivateKey = oldKey
		return err
	}
	m.cert.Store(c)
	return nil
}

func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert := m.cert.Load()
		if cert == nil {
			return nil, errors.New("HTTPS 证书未就绪")
		}
		return cert, nil
	}}
}

func (m *Manager) HasValidCertificate() bool {
	c := m.cert.Load()
	return c != nil && time.Until(c.Leaf.NotAfter) > 0 && time.Now().After(c.Leaf.NotBefore)
}

// Renew runs while the LAN server is active. Failed renewals retain valid certs.
func (m *Manager) Renew(ctx context.Context, report func(error)) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report(m.Ensure())
		}
	}
}
