package handoff

import (
	"net/http"
	"time"
)

var localTransport = &http.Transport{Proxy: nil, IdleConnTimeout: 90 * time.Second}
