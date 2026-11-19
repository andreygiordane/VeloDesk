// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Descoberta de dispositivos na rede local: cada VeloDesk avisa por broadcast UDP (a cada 3 s) quem ele é, e
// escuta os avisos dos outros. É só uma lista de sugestões: conectar continua exigindo senha.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const discDefaultPort = 21117

type discPacket struct {
	App  string `json:"app"`
	V    int    `json:"v"`
	ID   string `json:"id"`
	Name string `json:"name"`
	OS   string `json:"os"`
	Ver  string `json:"ver"`
	Srv  string `json:"srv"`
	Mac  string `json:"mac,omitempty"`
}

type discPeer struct {
	discPacket
	Seen time.Time
}

type discovery struct {
	mu    sync.Mutex
	peers map[string]discPeer
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// srvKey identifica o servidor de sinalização (só host:porta): dois aparelhos só se conectam se usam o mesmo.
func srvKey(server string) string {
	u, err := url.Parse(server)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || len(n.Mask) != net.IPv4len && len(n.Mask) != net.IPv6len {
				continue
			}
			ip, m := n.IP.To4(), n.Mask
			if len(m) == net.IPv6len {
				m = m[12:]
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^m[i]
			}
			out = append(out, b)
		}
	}
	return out
}

func startDiscovery(st *store) *discovery {
	d := &discovery{peers: map[string]discPeer{}}
	port := discDefaultPort
	if v := os.Getenv("VELODESK_DISC_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			port = n
		}
	}
	pc, err := (&net.ListenConfig{Control: reuseControl}).ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("discovery disabled: %v", err)
		return d
	}
	go d.listen(pc, st)
	go d.announce(pc, st, port)
	return d
}

func (d *discovery) listen(pc net.PacketConn, st *store) {
	buf := make([]byte, 2048)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if !prefBool(st.get().Prefs, "discScan", true) {
			continue
		}
		var p discPacket
		if json.Unmarshal(buf[:n], &p) != nil || p.App != "velodesk" || p.V != 1 || !validID(p.ID) || p.ID == st.get().ID {
			continue
		}
		p.Name, p.OS, p.Ver, p.Srv = clip(p.Name, 40), clip(p.OS, 12), clip(p.Ver, 20), clip(p.Srv, 80)
		if _, err := parseMAC(p.Mac); err != nil {
			p.Mac = ""
		}
		d.mu.Lock()
		d.peers[p.ID] = discPeer{p, time.Now()}
		d.mu.Unlock()
	}
}

func (d *discovery) announce(pc net.PacketConn, st *store, port int) {
	host, _ := os.Hostname()
	for ; ; time.Sleep(3 * time.Second) {
		c := st.get()
		if c.ID == "" || prefBool(c.Prefs, "discHide", false) {
			continue
		}
		pkt := discPacket{App: "velodesk", V: 1, ID: c.ID, Name: clip(host, 40), OS: runtime.GOOS, Ver: version, Srv: srvKey(c.Server)}
		if prefStr(c.Prefs, "wolMode", "on") != "off" {
			if m := selfMACs(); len(m) > 0 {
				pkt.Mac = m[0]
			}
		}
		b, _ := json.Marshal(pkt)
		for _, ip := range broadcastAddrs() {
			pc.WriteTo(b, &net.UDPAddr{IP: ip, Port: port})
		}
	}
}

func (d *discovery) list(selfSrv string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []map[string]any{}
	for id, p := range d.peers {
		if time.Since(p.Seen) > 12*time.Second {
			delete(d.peers, id)
			continue
		}
		out = append(out, map[string]any{"id": id, "name": p.Name, "os": p.OS, "ver": p.Ver, "sameServer": p.Srv != "" && p.Srv == selfSrv, "mac": p.Mac})
	}
	return out
}

func registerDiscovery(mux *http.ServeMux, st *store, d *discovery, guard func(http.HandlerFunc) http.HandlerFunc, jsonOut func(http.ResponseWriter, any)) {
	mux.HandleFunc("/api/discovery", guard(func(w http.ResponseWriter, r *http.Request) {
		scan := prefBool(st.get().Prefs, "discScan", true)
		list := []map[string]any{}
		if scan {
			list = d.list(srvKey(st.get().Server))
		}
		jsonOut(w, map[string]any{"scan": scan, "devices": list})
	}))
}
