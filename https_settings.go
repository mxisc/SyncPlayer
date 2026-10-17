package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syncplayer/internal/lanhttps"
)

var httpsSettingsMu sync.Mutex

type HTTPSStatus struct {
	ID       string `json:"id"`
	HasToken bool   `json:"has_token"`
	Domain   string `json:"domain"`
	Ready    bool   `json:"ready"`
	LocalIP  string `json:"local_ip"`
	Message  string `json:"message"`
}

func httpsSettingsDir() (string, error) {
	exe, err := os.Executable()
	return filepath.Join(filepath.Dir(exe), "settings", "https"), err
}

func (a *App) DeoHTTPSStatus() HTTPSStatus {
	httpsSettingsMu.Lock()
	defer httpsSettingsMu.Unlock()
	status := HTTPSStatus{}
	if conn, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		status.LocalIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
		conn.Close()
	}
	dir, err := httpsSettingsDir()
	if err != nil {
		status.Message = "无法读取程序目录"
		return status
	}
	m, err := lanhttps.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.Message = "HTTPS 配置无法读取，请在原 Windows 账户下使用，或重新配置"
		return status
	}
	status.Domain = m.Domain()
	id, token := m.Credentials()
	status.ID = id
	status.HasToken = token != ""
	status.Ready = m.CertificateReady()
	if !status.Ready {
		status.Message = "尚无有效证书，请填写凭据并重新申请"
	}
	return status
}

func (a *App) RevealDeoHTTPSToken() (string, error) {
	httpsSettingsMu.Lock()
	defer httpsSettingsMu.Unlock()
	dir, err := httpsSettingsDir()
	if err != nil {
		return "", errors.New("无法读取程序目录")
	}
	m, err := lanhttps.Open(dir)
	if err != nil {
		return "", errors.New("无法读取已保存的 Token")
	}
	_, token := m.Credentials()
	return token, nil
}

func (a *App) ConfigureDeoHTTPS(domain, id, token string, agree bool) error {
	httpsSettingsMu.Lock()
	defer httpsSettingsMu.Unlock()
	if !agree {
		return errors.New("请先同意证书服务条款")
	}
	dir, err := httpsSettingsDir()
	if err != nil {
		return errors.New("无法读取程序目录")
	}
	m, err := lanhttps.Configure(dir, lanhttps.Settings{Domain: strings.TrimSpace(domain), ID: strings.TrimSpace(id), Token: strings.TrimSpace(token)})
	if err != nil {
		return errors.New("保存失败：请检查域名、DNSPod ID / Token、目录权限及 Windows 账户")
	}
	if err := m.Ensure(); err != nil {
		return errors.New("证书申请失败：请检查 DNSPod Token 权限、域名托管及网络后重试；配置已在本地加密保存")
	}
	return nil
}
