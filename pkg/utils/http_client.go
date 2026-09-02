package utils

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// CreateHTTPClient creates an HTTP client with optional proxy support.
// If proxyURL is empty, it uses the system environment proxy settings.
// Supported proxy schemes: http, https, socks5, socks5h.
func CreateHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = config.Global().HTTPRequestTimeout()
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     config.Global().HTTPIdleConnTimeout(),
			DisableCompression:  false,
			TLSHandshakeTimeout: config.Global().HTTPTLSHandshakeTimeout(),
		},
	}

	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		scheme := strings.ToLower(proxy.Scheme)
		switch scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf(
				"unsupported proxy scheme %q (supported: http, https, socks5, socks5h)",
				proxy.Scheme,
			)
		}
		if proxy.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL: missing host")
		}
		tr, ok := client.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("internal error: transport is not *http.Transport")
		}
		tr.Proxy = http.ProxyURL(proxy)
	} else {
		tr, ok := client.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("internal error: transport is not *http.Transport")
		}
		tr.Proxy = http.ProxyFromEnvironment
	}

	return client, nil
}
