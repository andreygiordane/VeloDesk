// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Wake-On-LAN: pacote mágico (6×0xFF + 16×MAC) por broadcast UDP. O computador precisa ter WoL ligado na BIOS e na placa de rede.

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func magicPacket(mac net.HardwareAddr) []byte {
	b := bytes.Repeat([]byte{0xff}, 6)
	for i := 0; i < 16; i++ {
		b = append(b, mac...)
	}
	return b
}

func parseMAC(s string) (net.HardwareAddr, error) {
	m, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil || len(m) != 6 {
		return nil, errors.New("endereço MAC inválido")
	}
	return m, nil
}

// selfMACs lista os MACs das placas físicas ligadas (ignora loopback e adaptadores virtuais comuns).
func selfMACs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		n := strings.ToLower(ifc.Name)
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != 6 {
			continue
		}
		skip := false
		for _, v := range []string{"vethernet", "vmware", "virtualbox", "vbox", "tailscale", "docker", "wsl", "loopback", "bluetooth", "tap", "tun", "hyper-v"} {
			if strings.Contains(n, v) {
				skip = true
			}
		}
		if skip {
			continue
		}
		out = append(out, ifc.HardwareAddr.String())
	}
	return out
}

func wolPorts() []int {
	if v := os.Getenv("VELODESK_WOL_PORT"); v != "" { // permite teste sem a porta 9
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return []int{n}
		}
	}
	return []int{9, 7}
}

func sendMagic(mac net.HardwareAddr) error {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}
	defer pc.Close()
	pkt := magicPacket(mac)
	sent := 0
	for _, ip := range broadcastAddrs() {
		for _, port := range wolPorts() {
			if _, err := pc.WriteTo(pkt, &net.UDPAddr{IP: ip, Port: port}); err == nil {
				sent++
			}
		}
	}
	if sent == 0 {
		return errors.New("nenhuma rede disponível para enviar o pacote")
	}
	return nil
}

func registerWOL(mux *http.ServeMux, st *store, guard func(http.HandlerFunc) http.HandlerFunc,
	decode func(http.ResponseWriter, *http.Request, any) bool, jsonOut func(http.ResponseWriter, any)) {
	var mu sync.Mutex
	var last time.Time
	mux.HandleFunc("/api/wol/self", guard(func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]any{"macs": selfMACs(), "mode": prefStr(st.get().Prefs, "wolMode", "on")})
	}))
	mux.HandleFunc("/api/wol/send", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ MAC string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		m, err := parseMAC(in.MAC)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		tooSoon := time.Since(last) < 2*time.Second
		if !tooSoon {
			last = time.Now()
		}
		mu.Unlock()
		if tooSoon {
			http.Error(w, "aguarde alguns segundos antes de acordar de novo", http.StatusTooManyRequests)
			return
		}
		if err := sendMagic(m); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	}))
}
