package utils

import (
	"net"
)

func ParseIP(ipStr, maskStr string) net.IPNet {
	// Parse the IP
	ip := net.ParseIP(ipStr)
	ip = ip.To4()

	// Parse the subnet mask
	maskIP := net.ParseIP(maskStr)
	mask := net.IPMask(maskIP.To4())

	// Construct net.IPNet
	return net.IPNet{
		IP:   ip,
		Mask: mask,
	}
}
