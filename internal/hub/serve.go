package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/admin"
)

// Options for serving the hub.
type Options struct {
	Listen     string   // loopback address for the page, default 127.0.0.1:4790
	Public     string   // a domain name: serve HTTPS on :443 with Let's Encrypt, and :80 for redirects
	URL        string   // the public URL when a TLS proxy (Pangolin, Caddy, nginx...) forwards to Listen
	Email      string   // optional contact for Let's Encrypt
	DevOrigins []string // extra allowed origins, e.g. http://localhost:5173 for the Vite dev server
}

// AdminPath is the hub's admin socket.
func AdminPath() string { return filepath.Join(Dir(), "admin.sock") }

// Serve runs the hub until ctx ends.
func Serve(ctx context.Context, o Options) error {
	h, err := New()
	if err != nil {
		return err
	}
	if o.Listen == "" {
		o.Listen = "127.0.0.1:4790"
	}
	if o.URL != "" { // remember a URL given on the command line
		if err := h.store.Update(func(s *State) error { s.URL = strings.TrimRight(o.URL, "/"); return nil }); err != nil {
			return err
		}
	}
	listen, public := o.Listen, h.publicURL()
	if o.Public != "" {
		listen, public = "", "https://"+o.Public
	}
	w, err := NewWeb(h, listen, public, o.DevOrigins)
	if err != nil {
		return err
	}
	base := w.Base()
	go h.Run(ctx)
	go func() {
		if err := admin.Serve(ctx, AdminPath(), h.adminHandler(w)); err != nil {
			log.Printf("admin socket: %v", err)
		}
	}()
	log.Printf("hub %s: %s", h.Fingerprint()[:12], base)

	errc := make(chan error, 3)
	if o.Public != "" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(o.Public),
			Cache:      autocert.DirCache(filepath.Join(Dir(), "autocert")),
			Email:      o.Email,
		}
		cfg := m.TLSConfig()
		cfg.MinVersion = tls.VersionTLS12
		ln, err := tls.Listen("tcp", ":443", cfg)
		if err != nil {
			return fmt.Errorf("listen :443: %w (the service needs permission for low ports)", err)
		}
		go func() { errc <- w.Serve(ctx, &http.Server{}, ln) }()
		go func() {
			srv := &http.Server{Addr: ":80", Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
			go func() { <-ctx.Done(); srv.Close() }()
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf(":80 (redirects and certificate checks): %v", err)
			}
		}()
	}
	if o.Public == "" {
		ln, err := net.Listen("tcp", o.Listen)
		if err != nil {
			return err
		}
		go func() { errc <- w.Serve(ctx, &http.Server{}, ln) }()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

func (h *Hub) adminHandler(w *Web) admin.Handler {
	return func(ctx context.Context, op string, args json.RawMessage) (any, error) {
		var a struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		json.Unmarshal(args, &a)
		switch op {
		case "login-link":
			return map[string]string{"url": w.Base() + "/signin#" + h.NewSignin()}, nil
		case "link":
			link, l, err := h.CreateLink(ctx, a.Name)
			if err != nil {
				return nil, err
			}
			return map[string]any{"link": link, "command": w.installCommandFor(l.Name), "expires": l.Expires, "id": l.ID}, nil
		case "token":
			tok, err := h.CreateToken(a.Name)
			if err != nil {
				return nil, err
			}
			return map[string]string{"token": tok, "hub": w.Base()}, nil
		case "status":
			h.mu.Lock()
			connected := h.client != nil
			h.mu.Unlock()
			return map[string]any{"base": w.Base(), "relay": connected, "machines": h.Machines(), "links": h.Links()}, nil
		case "links":
			return h.Links(), nil
		case "set-url":
			var u struct {
				URL string `json:"url"`
			}
			json.Unmarshal(args, &u)
			u.URL = strings.TrimRight(strings.TrimSpace(u.URL), "/")
			if u.URL != "" && !strings.HasPrefix(u.URL, "https://") {
				return nil, errors.New("the public URL must start with https://")
			}
			if err := w.setURL(u.URL); err != nil {
				return nil, err
			}
			if err := h.store.Update(func(s *State) error { s.URL = u.URL; return nil }); err != nil {
				return nil, err
			}
			return map[string]string{"base": w.Base()}, nil
		case "cancel-link":
			return nil, h.CancelLink(a.ID)
		case "remove":
			return nil, h.RemoveDock(ctx, a.ID)
		}
		return nil, errors.New("unknown operation " + op)
	}
}

func (h *Hub) publicURL() string {
	var u string
	h.store.View(func(s *State) { u = s.URL })
	return u
}
