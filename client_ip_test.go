package httpserver

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveClientIP(t *testing.T) {
	testCases := map[string]struct {
		mutate  func(req *http.Request)
		want    string
		wantErr bool
	}{
		"legacy default": {
			want: "20.20.20.20",
		},
		"x real ip": {
			mutate: func(req *http.Request) {
				req.Header.Del(HeaderXForwardedFor)
			},
			want: "10.10.10.10",
		},
		"single x forwarded for": {
			mutate: func(req *http.Request) {
				req.Header.Set(HeaderXForwardedFor, "30.30.30.30  ")
			},
			want: "30.30.30.30",
		},
		"remote addr fallback": {
			mutate: func(req *http.Request) {
				req.Header.Del(HeaderXForwardedFor)
				req.Header.Del(HeaderXRealIP)
			},
			want: "40.40.40.40",
		},
		"remote addr without port": {
			mutate: func(req *http.Request) {
				req.Header.Del(HeaderXForwardedFor)
				req.Header.Del(HeaderXRealIP)
				req.RemoteAddr = "50.50.50.50"
			},
			wantErr: true,
		},
		"ipv6 remote addr": {
			mutate: func(req *http.Request) {
				req.RemoteAddr = "[::1]:12345"
			},
			want: "20.20.20.20",
		},
		"x forwarded for has non-ip element": {
			mutate: func(req *http.Request) {
				req.Header.Set(HeaderXForwardedFor, " blah ")
			},
			want: "10.10.10.10",
		},
		"x forwarded for invalid falls back to x real ip": {
			mutate: func(req *http.Request) {
				req.Header.Set(HeaderXForwardedFor, " blah ")
				req.Header.Set(HeaderXRealIP, " 10.10.10.10 ")
			},
			want: "10.10.10.10",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			req := requestForClientIPTest(t)

			if tc.mutate != nil {
				tc.mutate(req)
			}

			got, err := ResolveClientIP(req)
			if tc.wantErr {
				assert.Error(t, err)

				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func requestForClientIPTest(t *testing.T) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "/", http.NoBody)
	assert.NoError(t, err)

	req.Header.Set(HeaderXRealIP, " 10.10.10.10  ")
	req.Header.Set(HeaderXForwardedFor, "  20.20.20.20, 30.30.30.30")
	req.Header.Set("X-Appengine-Remote-Addr", "50.50.50.50")
	req.Header.Set("CF-Connecting-IP", "60.60.60.60")
	req.Header.Set("Fly-Client-IP", "70.70.70.70")
	req.RemoteAddr = "  40.40.40.40:42123 "

	return req
}
