// httpssetup imports DNSPod credentials from stdin without echoing secrets.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"syncplayer/internal/lanhttps"
)

func main() {
	dir := flag.String("dir", "build/bin/settings/https", "protected settings directory")
	domain := flag.String("domain", "", "certificate domain")
	flag.Parse()
	var credentials struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if json.NewDecoder(os.Stdin).Decode(&credentials) != nil {
		fmt.Fprintln(os.Stderr, "凭据输入无效")
		os.Exit(1)
	}
	m, err := lanhttps.Configure(*dir, lanhttps.Settings{Domain: *domain, ID: credentials.ID, Token: credentials.Token})
	if err == nil {
		err = m.Ensure()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("HTTPS 证书已签发并加密保存：", m.Domain())
}
