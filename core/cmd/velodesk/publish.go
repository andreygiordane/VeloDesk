// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Publisher tools: `velodesk keygen` creates the signing key pair (once);
// `velodesk publish` signs builds and writes the manifest the server distributes.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"velodesk/internal/update"
)

func runKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", ".", "directory for update.key (PRIVATE) and update.pub")
	fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	os.MkdirAll(*out, 0o700)
	kp, pp := filepath.Join(*out, "update.key"), filepath.Join(*out, "update.pub")
	if _, err := os.Stat(kp); err == nil {
		log.Fatalf("%s already exists; refusing to overwrite a signing key", kp)
	}
	must(os.WriteFile(kp, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600))
	must(os.WriteFile(pp, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644))
	fmt.Printf("chave privada (GUARDE E FAÇA BACKUP, nunca publique): %s\nchave pública (vai embutida nos apps):                  %s\n", kp, pp)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

type fileList map[string]*string

func runPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file (update.key)")
	ver := fs.String("version", "", "version being published, e.g. 0.3.0")
	out := fs.String("out", "updates", "output directory served by `velodesk server -updates`")
	notes := fs.String("notes", "", "release notes shown to users")
	channel := fs.String("channel", "stable", "stable | beta (beta writes manifest-beta.json)")
	builds := fileList{}
	for _, p := range []string{"linux-amd64", "windows-amd64", "darwin-arm64", "darwin-amd64", "linux-arm64"} {
		builds[p] = fs.String(p, "", "built binary for "+p)
	}
	fs.Parse(args)
	if *keyFile == "" || *ver == "" {
		log.Fatal("usage: velodesk publish -key update.key -version X.Y.Z [-linux-amd64 file] [-windows-amd64 file] ...")
	}
	kb, err := os.ReadFile(*keyFile)
	must(err)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(kb)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		log.Fatal("invalid private key file")
	}
	priv := ed25519.PrivateKey(raw)
	must(os.MkdirAll(*out, 0o755))

	m := update.Manifest{Version: *ver, Notes: *notes, Published: time.Now().UTC().Format(time.RFC3339), Files: map[string]update.File{}}
	for plat, pathp := range builds {
		if *pathp == "" {
			continue
		}
		sum, err := copyHashed(*pathp, filepath.Join(*out, nameFor(*ver, plat)))
		must(err)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, update.SigMessage(*ver, plat, sum)))
		m.Files[plat] = update.File{Name: nameFor(*ver, plat), SHA256: sum, Sig: sig}
		fmt.Printf("  %-14s %s\n", plat, sum[:16]+"…")
	}
	if len(m.Files) == 0 {
		log.Fatal("no binaries given")
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(*out, "manifest.json.tmp")
	must(os.WriteFile(tmp, b, 0o644))
	manifestName := "manifest.json"
	if *channel == "beta" {
		manifestName = "manifest-beta.json"
	}
	must(os.Rename(tmp, filepath.Join(*out, manifestName))) // last step: clients/servers see a complete release
	fmt.Printf("versão %s publicada em %s (o servidor avisa os clientes sozinho)\n", *ver, *out)
}

func nameFor(ver, plat string) string {
	n := "velodesk-" + ver + "-" + plat
	if strings.HasPrefix(plat, "windows") {
		n += ".exe"
	}
	return n
}

func copyHashed(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	o, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer o.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(o, h), in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
