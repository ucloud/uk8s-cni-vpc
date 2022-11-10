// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris
// +build darwin dragonfly freebsd linux netbsd openbsd solaris

// Read system DNS config from /etc/resolv.conf

package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

var defaultNS = []string{"127.0.0.1", "::1"}

// Bigger than we need, not too big to worry about overflow
const big = 0xFFFFFF

type DnsConfig struct {
	Servers    []string // servers to use
	Search     []string // suffixes to append to local name
	Ndots      int      // number of dots in name to trigger absolute lookup
	Timeout    int      // seconds before giving up on packet
	Attempts   int      // lost packets before giving up on server
	Rotate     bool     // round robin among servers
	UnknownOpt bool     // anything unknown was encountered
	Lookup     []string // OpenBSD top-level database "lookup" order
	Err        error    // any error that occurs during open of resolv.conf
}

// See resolv.conf(5) on a Linux machine.
// TODO(rsc): Supposed to call uname() and chop the beginning
// of the host name to get the default search domain.
func dnsReadConfig(filename string) *DnsConfig {
	conf := &DnsConfig{
		Ndots:    1,
		Timeout:  5,
		Attempts: 2,
	}
	file, err := os.Open(filename)
	if err != nil {
		conf.Servers = defaultNS
		conf.Err = err
		return conf
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 && (line[0] == ';' || line[0] == '#') {
			// comment.
			continue
		}
		f := strings.Fields(line)
		if len(f) < 1 {
			continue
		}
		switch f[0] {
		case "nameserver": // add one name server
			if len(f) > 1 && len(conf.Servers) < 3 { // small, but the standard limit
				// One more check: make sure server name is
				// just an IP address.  Otherwise we need DNS
				// to look it up.
				if net.ParseIP(f[1]) != nil {
					conf.Servers = append(conf.Servers, f[1])
				}
			}

		case "domain": // set search path to just this domain
			if len(f) > 1 {
				conf.Search = []string{f[1]}
			}

		case "search": // set search path to given servers
			conf.Search = make([]string, len(f)-1)
			for i := 0; i < len(conf.Search); i++ {
				conf.Search[i] = f[i+1]
			}

		case "options": // magic options
			for _, s := range f[1:] {
				switch {
				case strings.HasPrefix(s, "ndots:"):
					n, _, _ := dtoi(s, 6)
					if n < 1 {
						n = 1
					}
					conf.Ndots = n
				case strings.HasPrefix(s, "timeout:"):
					n, _, _ := dtoi(s, 8)
					if n < 1 {
						n = 1
					}
					conf.Timeout = n
				case strings.HasPrefix(s, "attempts:"):
					n, _, _ := dtoi(s, 9)
					if n < 1 {
						n = 1
					}
					conf.Attempts = n
				case s == "rotate":
					conf.Rotate = true
				default:
					conf.UnknownOpt = true
				}
			}

		case "lookup":
			// OpenBSD option:
			// http://www.openbsd.org/cgi-bin/man.cgi/OpenBSD-current/man5/resolv.conf.5
			// "the legal space-separated values are: bind, file, yp"
			conf.Lookup = f[1:]

		default:
			conf.UnknownOpt = true
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(conf.Servers) == 0 {
		conf.Servers = defaultNS
	}
	return conf
}

// Decimal to integer starting at &s[i0].
// Returns number, new offset, success.
func dtoi(s string, i0 int) (n int, i int, ok bool) {
	n = 0
	for i = i0; i < len(s) && '0' <= s[i] && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
		if n >= big {
			return 0, i, false
		}
	}
	if i == i0 {
		return 0, i, false
	}
	return n, i, true
}
