package httpclient

import (
	"context"
	"net"
	"net/http"
	"strings"

	tls "github.com/refraction-networking/utls"
)

// browserHelloSpec returns a Chrome ~133 ClientHello but with ALPN locked to
// http/1.1. The stock Chrome spec advertises h2; that's fine in a real
// browser, but Go's net/http.Transport decides HTTP version by type-asserting
// the post-TLS conn to *crypto/tls.Conn to read the negotiated ALPN. Our
// utls.UConn fails that assertion, so the transport reads the HTTP/2 SETTINGS
// frame as a malformed HTTP/1 response. Forcing ALPN to http/1.1 makes the
// server happily speak HTTP/1.1, which the transport handles natively.
func browserHelloSpec() *tls.ClientHelloSpec {
	spec, err := tls.UTLSIdToSpec(tls.HelloChrome_133)
	if err != nil {
		return nil
	}
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*tls.ALPNExtension); ok {
			a.AlpnProtocols = []string{"http/1.1"}
		}
	}
	return &spec
}

// BrowserTransport returns an http.RoundTripper whose TLS handshake mimics
// Chrome ~133. ADFS / D2L / most modern SSO stacks fingerprint the TLS
// ClientHello (JA3/JA4) and the HTTP/2 SETTINGS frame, and Go's stock
// crypto/tls produces fingerprints that trip bot-detection and downgrade
// the session to "non-browser" — which is why KMSI cookies and persistent
// MSISAuth never landed. Wrapping the dial in utls with a real Chrome
// profile fixes that.
func BrowserTransport() http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	dialTLS := base.DialTLSContext
	helloSpec := browserHelloSpec()
	base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if !looksTLS(addr, host) {
			if dialTLS != nil {
				return dialTLS(ctx, network, addr)
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		rawConn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		cfg := &tls.Config{ServerName: host}
		tlsConn := tls.UClient(rawConn, cfg, tls.HelloCustom)
		if err := tlsConn.ApplyPreset(helloSpec); err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	base.ForceAttemptHTTP2 = false
	return base
}

func looksTLS(addr, host string) bool {
	if strings.Contains(addr, "://") {
		return strings.HasPrefix(addr, "https://")
	}
	return !strings.HasSuffix(host, ":80") && !strings.HasSuffix(addr, ":80")
}
