package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
)

// Attach runs the stock herdr app against a remote machine's herdr session,
// through the hub and relay instead of SSH. spec is "machine" or
// "machine/user".
func Attach(ctx context.Context, spec, session string) error {
	c, err := Load()
	if err != nil {
		return err
	}
	machines, err := c.Fleet(ctx)
	if err != nil {
		return err
	}
	d, err := findDock(machines, spec)
	if err != nil {
		return err
	}
	if !d.Online {
		return fmt.Errorf("%s is offline", spec)
	}
	if session == "" {
		session = "default"
	}
	if !herdr.ValidSession(session) {
		return errors.New("bad session name")
	}
	bin, err := herdr.Binary()
	if err != nil {
		return err
	}

	// A private socket for herdr's client: 0600 in a 0700 directory.
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "ember-attach-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	os.Chmod(dir, 0o700)
	sock := filepath.Join(dir, "endpoint.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	os.Chmod(sock, 0o600)

	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				bridge(actx, c, d.ID, session, conn)
			}()
		}
	}()

	self, _ := os.Executable()
	reattach := shellQuote(self) + " attach " + shellQuote(spec)
	if session != "default" {
		reattach += " --session " + shellQuote(session)
	}
	// Keep the user's environment, including herdr's own nested-session
	// markers, so herdr's allow_nested guard still applies; only the socket
	// override goes.
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") || strings.HasPrefix(kv, "HERDR_CLIENT_SOCKET_PATH=") ||
			strings.HasPrefix(kv, "HERDR_REMOTE_KEYBINDINGS=") || strings.HasPrefix(kv, "HERDR_REATTACH_COMMAND=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HERDR_CLIENT_SOCKET_PATH="+sock,
		"HERDR_REMOTE_KEYBINDINGS=local",
		"HERDR_REATTACH_COMMAND="+reattach,
	)
	cmd := exec.CommandContext(ctx, bin, "client")
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	cancel()
	ln.Close()
	wg.Wait()
	return err
}

// bridge copies one herdr client connection to an endpoint channel.
func bridge(ctx context.Context, c Config, dock, session string, conn net.Conn) {
	defer conn.Close()
	ws, err := c.dial(ctx, "/api/endpoint", url.Values{"dock": {dock}, "session": {session}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ember-fleet: %v\r\n", err)
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	done := make(chan struct{}, 2)
	go func() { // remote -> local herdr
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				if uc, ok := conn.(*net.UnixConn); ok {
					uc.CloseWrite()
				}
				done <- struct{}{}
				return
			}
			if _, err := conn.Write(b); err != nil {
				done <- struct{}{}
				return
			}
		}
	}()
	go func() { // local herdr -> remote
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if ws.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					done <- struct{}{}
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					ws.Close(websocket.StatusNormalClosure, "")
				}
				done <- struct{}{}
				return
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
