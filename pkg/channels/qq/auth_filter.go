package qq

import (
	"net/http"
	"strings"

	"github.com/tencent-connect/botgo/openapi"
)

// botgo v0.2.1 composes the Authorization header through resty's
// SetAuthToken/SetAuthScheme inside an OnBeforeRequest hook, which runs after
// resty has already built the request — under resty >= v2.17 the scheme is
// dropped and every request goes out as "Bearer <token>", which the QQ
// gateway rejects with 401 "Authorization参数格式错误" (err_code 40011005).
// See upstream sipeed/picoclaw#3365.
//
// The token value itself is correct — only the scheme is wrong — so a botgo
// request filter that rewrites the header scheme is sufficient and avoids
// forking the dependency or pinning resty.

const qqAuthSchemeFilterName = "rhizome-qqbot-auth-scheme"

// RegisterQQAuthSchemeFilter installs the Authorization scheme fix on botgo's
// global request-filter chain. Registration is idempotent (botgo dedupes by
// name), so it is safe to call from package init and from channel start.
func RegisterQQAuthSchemeFilter() {
	openapi.RegisterReqFilter(qqAuthSchemeFilterName, fixQQBotAuthScheme)
}

// fixQQBotAuthScheme rewrites "Authorization: Bearer <token>" to the
// "QQBot <token>" scheme the QQ open platform requires. Requests without a
// Bearer header — or already carrying a correct scheme — pass through
// unchanged.
func fixQQBotAuthScheme(req *http.Request, _ *http.Response) error {
	const bearerPrefix = "Bearer "
	auth := req.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, bearerPrefix)
	if !ok || strings.TrimSpace(token) == "" {
		return nil
	}
	req.Header.Set("Authorization", "QQBot "+token)
	return nil
}
