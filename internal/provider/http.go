package provider

import (
	"net"
	"net/http"
	"time"
)

var (
	localTransport    = &http.Transport{Proxy: nil, IdleConnTimeout: 90 * time.Second}
	providerTransport = &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
		IdleConnTimeout: 90 * time.Second,
	}
	titleTransport = &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
		IdleConnTimeout: 90 * time.Second,
	}
)
