// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

// VeloDesk: remote desktop. One binary, two roles:
//
//	velodesk            the app (run on every machine; can control and be controlled)
//	velodesk server     the signaling server (run once on a reachable machine)
package main

import (
	"fmt"
	"os"
)

// version is injected at build time (-ldflags "-X main.version=1.2.3"); "dev" disables auto-update.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "server":
			runServer(os.Args[2:])
			return
		case "version", "-version", "--version":
			fmt.Println(version)
			return
		case "keygen":
			runKeygen(os.Args[2:])
			return
		case "publish":
			runPublish(os.Args[2:])
			return
		case "help", "-h", "--help":
			fmt.Println("uso:\n  velodesk [-server wss://host]   abre o app (use em todas as máquinas)\n  velodesk server [-addr :21116]  servidor de sinalização (rode uma vez)\n  velodesk keygen / publish       publicar atualizações assinadas")
			return
		}
	}
	runClient(os.Args[1:])
}
