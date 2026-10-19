package hub

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

func TestPINSignin(t *testing.T) {
	h := testHub(t)
	if set, _ := h.PINSet(); set {
		t.Fatal("a new hub has a PIN")
	}
	if err := h.checkPIN("12345678"); err == nil {
		t.Fatal("a PIN worked with none set")
	}
	pin, err := h.SetPIN("")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{4}-\d{4}$`).MatchString(pin) {
		t.Fatalf("made PIN %q, want 1234-5678", pin)
	}
	// Spaces, dashes and case don't matter.
	for _, typed := range []string{pin, pin[:4] + pin[5:], " " + pin[:4] + " " + pin[5:] + " "} {
		if err := h.checkPIN(typed); err != nil {
			t.Fatalf("%q: %v", typed, err)
		}
	}
	if _, err := h.SetPIN("abc"); err == nil {
		t.Fatal("a 3-character PIN was accepted")
	}
	if _, err := h.SetPIN("tea-pot-42"); err != nil {
		t.Fatal(err)
	}
	if err := h.checkPIN("TEAPOT42"); err != nil {
		t.Fatal("a chosen PIN didn't work in capitals:", err)
	}
	if err := h.checkPIN(pin); !errors.Is(err, errPINWrong) {
		t.Fatal("the old PIN still works:", err)
	}
}

func TestPINLocksAfterWrongGuesses(t *testing.T) {
	h := testHub(t)
	pin, _ := h.SetPIN("")
	for i := 0; i < pinFailShort; i++ {
		if err := h.checkPIN("00000000x"); !errors.Is(err, errPINWrong) {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	var locked *pinLocked
	if err := h.checkPIN(pin); !errors.As(err, &locked) {
		t.Fatal("the right PIN worked during a lockout:", err)
	}
	// The window passes: the right PIN works again.
	h.pinMu.Lock()
	for i := range h.pinFails {
		h.pinFails[i] = h.pinFails[i].Add(-16 * time.Minute)
	}
	h.pinMu.Unlock()
	if err := h.checkPIN(pin); err != nil {
		t.Fatal("still locked after 15 minutes:", err)
	}
	// Twenty in a day lock it for longer.
	h.pinMu.Lock()
	for len(h.pinFails) < pinFailDay {
		h.pinFails = append(h.pinFails, time.Now().Add(-2*time.Hour))
	}
	h.pinMu.Unlock()
	if err := h.checkPIN(pin); !errors.As(err, &locked) || locked.wait < time.Hour {
		t.Fatal("twenty wrong PINs in a day didn't pause PIN sign-in:", err)
	}
	if err := h.RemovePIN(); err != nil {
		t.Fatal(err)
	}
	if set, _ := h.PINSet(); set {
		t.Fatal("removed PIN is still set")
	}
}

func TestSignOutAnotherBrowser(t *testing.T) {
	h := testHub(t)
	mine, _, _ := h.startSession("Chrome on Linux", "link")
	laptop, _, err := h.startSession("Safari on macOS", "pin")
	if err != nil {
		t.Fatal(err)
	}
	me := "session:" + hashToken(mine)
	list := h.BrowserSessions(me)
	if len(list) != 2 {
		t.Fatalf("listed %d browsers, want 2", len(list))
	}
	var target SessionInfo
	for _, s := range list {
		if s.Current != (s.Agent == "Chrome on Linux") {
			t.Fatalf("wrong current flag: %+v", s)
		}
		if s.Agent == "Safari on macOS" {
			target = s
		}
	}
	if target.Via != "pin" || len(target.ID) != 16 {
		t.Fatalf("laptop listed as %+v", target)
	}
	s := h.session(laptop)
	ctx, cancel, err := h.credContext(context.Background(), principal{kind: "browser", cred: "session:" + s.Hash, expires: time.UnixMilli(s.Expires)})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := h.EndSessionID(target.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("signing out the laptop didn't cut its sockets")
	}
	if h.session(laptop) != nil || h.session(mine) == nil {
		t.Fatal("signed out the wrong browser")
	}
	if err := h.EndSessionID("short"); err == nil {
		t.Fatal("a partial ID matched a session")
	}
}
