// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Papel de parede do computador, em miniatura: é o que a tela inicial mostra para cada máquina acessada
// (como no AnyDesk). Só é enviado para quem está numa sessão aceita.

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"

	"golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
)

var _ = bmp.Decode // registra o formato BMP

// thumbJPEG reduz a imagem para 320×200 (recorte central, proporção 16:10) e codifica em JPEG.
func thumbJPEG(r io.Reader) ([]byte, error) {
	img, _, err := image.Decode(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() < 2 || b.Dy() < 2 {
		return nil, errors.New("imagem pequena demais")
	}
	// recorte central 16:10
	cw, ch := b.Dx(), b.Dx()*10/16
	if ch > b.Dy() {
		ch, cw = b.Dy(), b.Dy()*16/10
	}
	x0, y0 := b.Min.X+(b.Dx()-cw)/2, b.Min.Y+(b.Dy()-ch)/2
	dst := image.NewRGBA(image.Rect(0, 0, 320, 200))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(image.Black), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, image.Rect(x0, y0, x0+cw, y0+ch), xdraw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 72}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func registerWallpaper(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc, agentTok string) {
	mux.HandleFunc("/api/wallpaper", guard(func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		p := wallpaperPath()
		f, err := os.Open(p)
		if p == "" || err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		b, err := thumbJPEG(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(b)
	}))
}
