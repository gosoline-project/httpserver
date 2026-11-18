package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clientIpTestCase struct {
	trustedCIDRs  []string
	remoteAddr    string
	forwardedFor  []string
	realIP        string
	wantRemote    string
	wantForwarded string
	wantErr       bool
}

type ipTypeEntry struct {
	name  string
	value IpType
	want  string
}

func getClientIpTestCases() map[string]clientIpTestCase {
	trustedProxies := []string{"40.40.40.40/32", "30.30.30.30/32", "2001:db8:ffff::/48"}

	return map[string]clientIpTestCase{
		"trusted proxy chain": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"  20.20.20.20, 30.30.30.30  "},
			realIP:        "10.10.10.10",
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"stop at first untrusted proxy": {
			trustedCIDRs:  []string{"40.40.40.40/32"},
			forwardedFor:  []string{"20.20.20.20, 30.30.30.30"},
			wantRemote:    "30.30.30.30",
			wantForwarded: "20.20.20.20",
		},
		"untrusted direct peer ignores both headers": {
			trustedCIDRs:  []string{"30.30.30.30/32"},
			forwardedFor:  []string{"20.20.20.20, 30.30.30.30"},
			realIP:        "10.10.10.10",
			wantRemote:    "40.40.40.40",
			wantForwarded: "20.20.20.20",
		},
		"missing trust context": {
			forwardedFor:  []string{"20.20.20.20"},
			wantRemote:    "40.40.40.40",
			wantForwarded: "20.20.20.20",
		},
		"empty trust list": {
			trustedCIDRs:  []string{},
			forwardedFor:  []string{"20.20.20.20"},
			wantRemote:    "40.40.40.40",
			wantForwarded: "20.20.20.20",
		},
		"trust all preserves legacy behavior": {
			trustedCIDRs:  []string{"0.0.0.0/0", "::/0"},
			forwardedFor:  []string{"20.20.20.20, 30.30.30.30"},
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"all forwarded addresses are trusted": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"30.30.30.30, 40.40.40.40"},
			wantRemote:    "30.30.30.30",
			wantForwarded: "30.30.30.30",
		},
		"CIDR includes direct peer": {
			trustedCIDRs:  []string{"40.40.40.0/24"},
			forwardedFor:  []string{"20.20.20.20"},
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"direct peer outside CIDR": {
			trustedCIDRs:  []string{"40.40.41.0/24"},
			forwardedFor:  []string{"20.20.20.20"},
			wantRemote:    "40.40.40.40",
			wantForwarded: "20.20.20.20",
		},
		"single forwarded address": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{" 20.20.20.20 "},
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"XFF takes precedence over X real IP": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"20.20.20.20"},
			realIP:        "10.10.10.10",
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"X real IP fallback": {
			trustedCIDRs:  trustedProxies,
			realIP:        " 10.10.10.10 ",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"empty XFF falls back to X real IP": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{""},
			realIP:        "10.10.10.10",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"invalid XFF falls back to X real IP": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{" blah "},
			realIP:        "10.10.10.10",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"missing headers fall back to remote address": {
			trustedCIDRs:  trustedProxies,
			wantRemote:    "40.40.40.40",
			wantForwarded: "40.40.40.40",
		},
		"invalid headers fall back to remote address": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"invalid"},
			realIP:        "also invalid",
			wantRemote:    "40.40.40.40",
			wantForwarded: "40.40.40.40",
		},
		"invalid closest hop breaks chain": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"20.20.20.20, invalid"},
			wantRemote:    "40.40.40.40",
			wantForwarded: "40.40.40.40",
		},
		"invalid distant hop beyond untrusted boundary": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"invalid, 20.20.20.20, 30.30.30.30"},
			realIP:        "10.10.10.10",
			wantRemote:    "20.20.20.20",
			wantForwarded: "10.10.10.10",
		},
		"invalid distant hop after trusted hops breaks chain": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"invalid, 30.30.30.30"},
			realIP:        "10.10.10.10",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"empty closest hop breaks chain": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"20.20.20.20,"},
			wantRemote:    "40.40.40.40",
			wantForwarded: "40.40.40.40",
		},
		"repeated XFF lines form one ordered chain": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"10.10.10.10", "20.20.20.20, 30.30.30.30"},
			wantRemote:    "20.20.20.20",
			wantForwarded: "10.10.10.10",
		},
		"invalid later XFF line cannot be skipped": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"20.20.20.20", "invalid"},
			realIP:        "10.10.10.10",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"IPv4 ports are stripped before trust checks": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"20.20.20.20:1234, 30.30.30.30:5678"},
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"X real IP with port": {
			trustedCIDRs:  trustedProxies,
			realIP:        "10.10.10.10:1234",
			wantRemote:    "10.10.10.10",
			wantForwarded: "10.10.10.10",
		},
		"bare IPv6 forwarded address": {
			trustedCIDRs:  trustedProxies,
			forwardedFor:  []string{"2001:db8::1234"},
			wantRemote:    "2001:db8::1234",
			wantForwarded: "2001:db8::1234",
		},
		"IPv6 ports are stripped before trust checks": {
			trustedCIDRs:  trustedProxies,
			remoteAddr:    "[2001:db8:ffff::1]:443",
			forwardedFor:  []string{"[2001:db8::7]:1234, [2001:db8:ffff::2]:5678"},
			wantRemote:    "2001:db8::7",
			wantForwarded: "2001:db8::7",
		},
		"untrusted IPv6 direct peer": {
			trustedCIDRs:  trustedProxies,
			remoteAddr:    "[2001:db8:abcd::1]:443",
			forwardedFor:  []string{"2001:db8::7"},
			wantRemote:    "2001:db8:abcd::1",
			wantForwarded: "2001:db8::7",
		},
		"IPv6 remote address fallback": {
			trustedCIDRs:  trustedProxies,
			remoteAddr:    "[2001:db8:ffff::1]:443",
			wantRemote:    "2001:db8:ffff::1",
			wantForwarded: "2001:db8:ffff::1",
		},
		"IPv4 mapped direct peer matches IPv4 CIDR": {
			trustedCIDRs:  trustedProxies,
			remoteAddr:    "[::ffff:40.40.40.40]:443",
			forwardedFor:  []string{"20.20.20.20"},
			wantRemote:    "20.20.20.20",
			wantForwarded: "20.20.20.20",
		},
		"remote address without port": {
			trustedCIDRs: trustedProxies,
			remoteAddr:   "40.40.40.40",
			forwardedFor: []string{"20.20.20.20"},
			wantErr:      true,
		},
		"remote host is not an IP": {
			trustedCIDRs: trustedProxies,
			remoteAddr:   "invalid:443",
			forwardedFor: []string{"20.20.20.20"},
			wantErr:      true,
		},
	}
}

func TestResolveClientIP(t *testing.T) {
	testCases := getClientIpTestCases()

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

func (tc clientIpTestCase) run(t *testing.T) {
	req := requestForClientIPTest(t)
	for _, value := range tc.forwardedFor {
		req.Header.Add(HeaderXForwardedFor, value)
	}
	if tc.realIP != "" {
		req.Header.Set(HeaderXRealIP, tc.realIP)
	}
	if tc.remoteAddr != "" {
		req.RemoteAddr = tc.remoteAddr
	}

	// Exercise the middleware that supplies the resolver's trust configuration.
	if tc.trustedCIDRs != nil {
		middleware, err := NewSetTrustedProxiesMiddleware(tc.trustedCIDRs)
		require.NoError(t, err)
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = req
		middleware(ginCtx)
		req = ginCtx.Request
	}

	for _, ipType := range []ipTypeEntry{
		{name: "RemoteIp", value: RemoteIp, want: tc.wantRemote},
		{name: "ForwardedIp", value: ForwardedIp, want: tc.wantForwarded},
	} {
		t.Run(ipType.name, func(t *testing.T) {
			got, err := ResolveClientIP(req, ipType.value)
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, ipType.want, got)
		})
	}
}

func requestForClientIPTest(t *testing.T) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "/", http.NoBody)
	require.NoError(t, err)

	// These platform-specific headers must never override the supported sources.
	req.Header.Set("X-Appengine-Remote-Addr", "50.50.50.50")
	req.Header.Set("CF-Connecting-IP", "60.60.60.60")
	req.Header.Set("Fly-Client-IP", "70.70.70.70")
	req.RemoteAddr = "  40.40.40.40:42123 "

	return req
}
