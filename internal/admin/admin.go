// Package admin is a tiny request/response channel between the CLI and a
// running hub or relay of the same OS user: a Unix socket (0600 in a 0700
// directory), one JSON request and one JSON reply per connection.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

// Handler answers one admin operation.
type Handler func(ctx context.Context, op string, args json.RawMessage) (any, error)

type request struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type reply struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Serve listens on path until ctx ends.
func Serve(ctx context.Context, path string, h Handler) error {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	os.Chmod(path, 0o600)
	go func() {
		<-ctx.Done()
		ln.Close()
		os.Remove(path)
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			defer c.Close()
			if uc, ok := c.(*net.UnixConn); ok && !sameUser(uc) {
				return
			}
			c.SetDeadline(time.Now().Add(30 * time.Second))
			var req request
			if err := json.NewDecoder(c).Decode(&req); err != nil {
				return
			}
			res, err := h(ctx, req.Op, req.Args)
			out := reply{OK: err == nil}
			if err != nil {
				out.Error = err.Error()
			} else {
				out.Result, _ = json.Marshal(res)
			}
			json.NewEncoder(c).Encode(out)
		}()
	}
}

// Call sends one operation to the service listening on path.
func Call(path, op string, args any, result any) error {
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return ErrNotRunning
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	a, _ := json.Marshal(args)
	if err := json.NewEncoder(c).Encode(request{Op: op, Args: a}); err != nil {
		return err
	}
	var out reply
	if err := json.NewDecoder(c).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New(out.Error)
	}
	if result != nil {
		return json.Unmarshal(out.Result, result)
	}
	return nil
}

// ErrNotRunning means nothing is listening.
var ErrNotRunning = errors.New("not running")
