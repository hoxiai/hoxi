package app

import "net"

func loopbackUIOrigins(addr *net.TCPAddr) []string {
	// Keep the literal address family: IPv4 listeners cannot serve ::1.
	origins := []string{"http://" + addr.String()}
	if !addr.IP.IsLoopback() {
		return origins
	}
	_, port, _ := net.SplitHostPort(addr.String())
	origins = append(origins, "http://"+net.JoinHostPort("localhost", port))
	return origins
}
