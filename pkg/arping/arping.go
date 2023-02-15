// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package arping

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"syscall"
	"time"
)

const (
	ProbeWaitMax = 1000 // ms
	ProbeMin     = 1000 // ms
	ProbeMax     = 2000 // ms
	AnnounceWait = 2000 // ms
	MaxConflicts = 10
	ProbeNum     = 3
)

type socket interface {
	send(request arpDatagram) (time.Time, error)
	receive() (arpDatagram, time.Time, error)
	deinitialize() error
}

var timeout = time.Duration(500 * time.Millisecond)

func DetectIpConflictWithGratuitousArp(srcIP net.IP, ifaceName string) (bool, error) {
	var conflict bool
	var err error
	for i := 0; i < MaxConflicts; i++ {
		conflict, err = detectIpConflictWithGratuitousArp(srcIP, ifaceName)
		if err != nil {
			continue
		}
		if !conflict {
			return false, nil
		}
	}
	return conflict, err
}

type result struct {
	conflict bool
	err      error
}

// detectIpConflictWithGratuitousArp detect ip conflict according to rfc5227
func detectIpConflictWithGratuitousArp(srcIP net.IP, ifaceName string) (bool, error) {
	if err := validateIP(srcIP); err != nil {
		return false, fmt.Errorf("fail to validate ip: %w", err)
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return false, fmt.Errorf("fail to get interface by name: %w", err)
	}

	sock, err := initialize(*iface)
	if err != nil {
		return false, fmt.Errorf("fail to initialize interface %v: %w", ifaceName, err)
	}
	defer sock.deinitialize()
	// implement 2.1.1. section in rfc5227
	time.Sleep(time.Duration(rand.Intn(ProbeWaitMax)) * time.Millisecond)
	// send garp
	announceWaitChan := make(chan time.Time, 1)
	stopSend := make(chan interface{}, 1)
	go func() {
		for i := 0; i < ProbeNum; i++ {
			select {
			case <-stopSend:
				return
			default:
				gratuitousArpOverIface(sock, srcIP, *iface)
				if i != ProbeNum-1 {
					time.Sleep(time.Duration(rand.Intn(ProbeMax-ProbeMin)+ProbeMin) * time.Millisecond)
				}
			}
		}
		select {
		case <-time.After(time.Duration(AnnounceWait) * time.Millisecond):
			announceWaitChan <- time.Now()
		}
	}()
	// receive response
	stopReceive := make(chan interface{}, 1)
	resultChan := make(chan result, 1)
	go func() {
		for {
			select {
			case <-stopReceive:
				return
			default:
				dg, err := receiveGratuitousArpResponse(sock)
				if err != nil {
					if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
						resultChan <- result{false, fmt.Errorf("fail to receive gratuitous arp response: %w", err)}
						return
					}
				} else {
					// just support ipv4
					if bytes.Compare(dg.spa, srcIP.To4()) == 0 {
						resultChan <- result{true, nil}
						return
					}
					if bytes.Compare(dg.tpa, srcIP.To4()) == 0 && bytes.Compare(dg.sha, iface.HardwareAddr) != 0 {
						resultChan <- result{true, nil}
						return
					}
				}
			}
		}
	}()
	select {
	case r := <-resultChan:
		stopSend <- true
		return r.conflict, r.err
	case <-announceWaitChan:
		stopReceive <- true
	}
	return false, nil
}

// gratuitousArpOverIface sends an gratuitous arp over interface 'iface' from 'srcIP'
func gratuitousArpOverIface(sock socket, srcIP net.IP, iface net.Interface) error {
	if err := validateIP(srcIP); err != nil {
		return err
	}

	srcMac := iface.HardwareAddr
	broadcastMac := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	request := newArpRequest(srcMac, srcIP, broadcastMac, srcIP)
	_, err := sock.send(request)
	return err
}

func receiveGratuitousArpResponse(sock socket) (arpDatagram, error) {
	datagram, _, err := sock.receive()
	return datagram, err
}

func validateIP(ip net.IP) error {
	// ip must be a valid V4 address
	if len(ip.To4()) != net.IPv4len {
		return fmt.Errorf("not a valid v4 Address: %s", ip)
	}
	return nil
}

type LinuxSocket struct {
	sock       int
	toSockaddr syscall.SockaddrLinklayer
}

func initialize(iface net.Interface) (s *LinuxSocket, err error) {
	s = &LinuxSocket{}
	s.toSockaddr = syscall.SockaddrLinklayer{Ifindex: iface.Index}

	// 1544 = htons(ETH_P_ARP)
	const proto = 1544
	s.sock, err = syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, proto)
	return s, err
}

func (s *LinuxSocket) send(request arpDatagram) (time.Time, error) {
	return time.Now(), syscall.Sendto(s.sock, request.MarshalWithEthernetHeader(), 0, &s.toSockaddr)
}

func (s *LinuxSocket) receive() (arpDatagram, time.Time, error) {
	buffer := make([]byte, 128)
	socketTimeout := timeout.Nanoseconds() * 2
	t := syscall.NsecToTimeval(socketTimeout)
	syscall.SetsockoptTimeval(s.sock, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &t)
	n, _, err := syscall.Recvfrom(s.sock, buffer, 0)
	if err != nil {
		return arpDatagram{}, time.Now(), err
	}
	// skip 14 bytes ethernet header
	return parseArpDatagram(buffer[14:n]), time.Now(), nil
}

func (s *LinuxSocket) deinitialize() error {
	return syscall.Close(s.sock)
}
