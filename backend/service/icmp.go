package service

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ICMP echo, used to time servers that speak only UDP — WireGuard, TUIC and the
// Hysteria pair. A TCP dial to their port measures nothing but the timeout,
// because no TCP listener is there to accept it.
//
// Windows exposes ICMP through iphlpapi rather than a socket, which is what
// makes this possible at all: raw sockets need administrator rights, while
// IcmpSendEcho works for any process. That matters because NeoBox runs
// unelevated whenever TUN mode is off.

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

// ipOptionInformation mirrors IP_OPTION_INFORMATION.
type ipOptionInformation struct {
	TTL         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

// icmpEchoReply mirrors ICMP_ECHO_REPLY. Field order and types have to match
// the C layout exactly — the API writes into this memory.
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

// icmpStatusSuccess is IP_SUCCESS: the echo came back.
const icmpStatusSuccess = 0

// icmpEchoLatency returns the round-trip time to host in milliseconds, or -1
// when the host does not answer ICMP. A great many servers drop echo requests
// on purpose, so -1 here means "cannot tell", not "server is down".
func icmpEchoLatency(host string, timeout time.Duration) int {
	ip, err := resolveIPv4(host, timeout)
	if err != nil {
		return -1
	}

	handle, _, _ := procIcmpCreateFile.Call()
	if handle == uintptr(windows.InvalidHandle) || handle == 0 {
		return -1
	}
	defer procIcmpCloseHandle.Call(handle)

	// A 32-byte payload is what ping.exe sends. The reply buffer has to hold
	// the reply struct, the echoed payload and room for an ICMP error message,
	// which is what the extra slack covers.
	request := make([]byte, 32)
	reply := make([]byte, unsafe.Sizeof(icmpEchoReply{})+uintptr(len(request))+64)

	replies, _, _ := procIcmpSendEcho.Call(
		handle,
		// IPAddr is the four address bytes in network order, which on a
		// little-endian machine is exactly what LittleEndian.Uint32 produces.
		uintptr(binary.LittleEndian.Uint32(ip)),
		uintptr(unsafe.Pointer(&request[0])),
		uintptr(len(request)),
		0, // no IP options
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(timeout.Milliseconds()),
	)
	if replies == 0 {
		return -1
	}

	echo := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
	if echo.Status != icmpStatusSuccess {
		return -1
	}
	return int(echo.RoundTripTime)
}

// resolveIPv4 turns a host into a four-byte address. IPv6 is not attempted:
// timing it needs Icmp6SendEcho2 and a source address, and every server NeoBox
// has met so far is reachable over IPv4.
func resolveIPv4(host string, timeout time.Duration) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, nil
		}
		return nil, errors.New("icmp: IPv6 hosts are not supported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if ip4 := addr.IP.To4(); ip4 != nil {
			return ip4, nil
		}
	}
	return nil, errors.New("icmp: host has no IPv4 address")
}
