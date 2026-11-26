package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

const testTasksToken = "secret-token"

func authedRequest(method, target string, body *strings.Reader) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Authorization", "Bearer "+testTasksToken)
	return req
}
