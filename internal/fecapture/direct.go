package fecapture

import (
	"net/http"
	"time"
)

// NewDirect is a client for the owner's own token (personal mode): Accept and Complete with
// a bearer the caller supplies, no issuer exchange. The HTTP settings match New: no cookies,
// no redirects, a short timeout.
func NewDirect(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	clientCopy.Timeout = 10 * time.Second
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &clientCopy}
}
