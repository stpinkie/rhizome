package internal

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/stpinkie/rhizome/pkg/pid"
)

// DaemonBaseURL returns the running daemon's gateway base URL and bearer
// token from the pid file, or ("", "") when no daemon is running.
func DaemonBaseURL() (base, token string) {
	data := pid.ReadPidFileWithCheck(GetRhizomeHome())
	if data == nil || data.Port == 0 || data.Token == "" {
		return "", ""
	}
	host := data.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(data.Port))), data.Token
}

// DaemonRequest performs an authenticated request against the daemon's
// gateway and returns the response body and status code.
func DaemonRequest(method, path string, body []byte, timeout time.Duration) ([]byte, int, error) {
	base, token := DaemonBaseURL()
	if base == "" {
		return nil, 0, fmt.Errorf("no running daemon found")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}
