// Package fallback discovers filetrans peers on LAN when USB is unavailable.
package fallback

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"filetrans/backend/config"
)

// Scan searches for a filetrans receiver listening on cfg.Port across the
// subnets listed in cfg.ScanSubnets. Returns IPs that responded.
// Scan is concurrent; each host gets a 500 ms connect timeout.
func Scan(cfg *config.Config) ([]string, error) {
	hosts, err := hostsFromSubnets(cfg.ScanSubnets)
	if err != nil {
		return nil, err
	}

	// Also include IPs from active local interfaces that aren't loopback.
	localNets, _ := localInterfaceNets()
	for _, ln := range localNets {
		extra := hostsInNet(ln)
		hosts = append(hosts, extra...)
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts to scan")
	}

	results := make(chan string, len(hosts))
	sem := make(chan struct{}, 128) // cap concurrency
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	for _, host := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			addr := fmt.Sprintf("%s:%d", h, cfg.Port)
			d := net.Dialer{Timeout: 500 * time.Millisecond}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err == nil {
				conn.Close()
				results <- h
			}
		}(host)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var found []string
	for ip := range results {
		found = append(found, ip)
	}
	return found, nil
}

// hostsFromSubnets expands each CIDR into individual host addresses.
func hostsFromSubnets(cidrs []string) ([]string, error) {
	var hosts []string
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue // skip malformed entries
		}
		hosts = append(hosts, hostsInNet(network)...)
	}
	return hosts, nil
}

// hostsInNet enumerates usable host addresses in a network (skips network + broadcast).
func hostsInNet(network *net.IPNet) []string {
	var hosts []string
	ip := cloneIP(network.IP)
	inc(ip)
	for network.Contains(ip) {
		next := cloneIP(ip)
		inc(next)
		if !network.Contains(next) {
			break // skip broadcast
		}
		hosts = append(hosts, ip.String())
		ip = next
	}
	return hosts
}

// localInterfaceNets returns all non-loopback IPv4 networks on local interfaces.
func localInterfaceNets() ([]*net.IPNet, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var nets []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			nets = append(nets, &net.IPNet{IP: ipNet.IP.Mask(ipNet.Mask), Mask: ipNet.Mask})
		}
	}
	return nets, nil
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
