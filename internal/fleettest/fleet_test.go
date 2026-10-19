// Package fleettest runs a relay, a hub and docks in one process and checks the
// properties the fleet's security rests on.
package fleettest

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/dock"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/hub"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
)

type fleet struct {
	t     *testing.T
	ctx   context.Context
	root  string
	relay *relay.Server
	hub   *hub.Hub
}

// home switches the process to a role's own config folder.
func (f *fleet) home(name string) {
	dir := filepath.Join(f.root, name)
	os.MkdirAll(dir, 0o700)
	os.Setenv("EMBER_FLEET_HOME", dir)
}

func start(t *testing.T) *fleet {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))  // no herdr sessions here
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache")) // uploads land here
	t.Setenv("EMBER_FLEET_HOME", "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &fleet{t: t, ctx: ctx, root: root}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	f.home("relay")
	link, err := relay.Init(addr, addr)
	if err != nil {
		t.Fatal(err)
	}
	f.relay, err = relay.Open(relay.Dir())
	if err != nil {
		t.Fatal(err)
	}
	go f.relay.Serve(ctx, ln)

	f.home("hub")
	if _, err := hub.Init(ctx, link, "Test"); err != nil {
		t.Fatal(err)
	}
	f.hub, err = hub.New()
	if err != nil {
		t.Fatal(err)
	}
	go f.hub.Run(ctx)
	f.eventually("hub connects to the relay", func() bool {
		_, _, err := f.hub.CreateLink(ctx, "probe")
		return err == nil
	})
	return f
}

func (f *fleet) eventually(what string, ok func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.t.Fatalf("timed out waiting: %s", what)
}

// joinDock joins a dock in its own folder and runs it.
func (f *fleet) joinDock(name, link string) (*dock.Dock, string) {
	f.t.Helper()
	f.home("dock-" + name)
	if _, err := dock.Join(f.ctx, link); err != nil {
		f.t.Fatalf("join %s: %v", name, err)
	}
	c, _ := dock.LoadConfig()
	c.KeepRunning = false
	c.Save()
	id, _ := keys.Load(dock.Dir())
	d, err := dock.New()
	if err != nil {
		f.t.Fatal(err)
	}
	go d.Run(f.ctx)
	return d, id.Fingerprint
}

func (f *fleet) paired(fp string) bool {
	for _, m := range f.hub.Machines() {
		for _, d := range m.Docks {
			if d.ID == fp {
				return true
			}
		}
	}
	return false
}

func (f *fleet) onlineWithInfo(fp string) bool {
	for _, m := range f.hub.Machines() {
		for _, d := range m.Docks {
			if d.ID == fp && d.Online && d.User != "" {
				return true
			}
		}
	}
	return false
}

func TestPairAndWatch(t *testing.T) {
	f := start(t)
	link, _, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, fp := f.joinDock("a", link)
	f.eventually("the dock is paired and reports its info", func() bool { return f.onlineWithInfo(fp) })
	if m := f.hub.Machines(); len(m) != 1 || m[0].Host != "studio" {
		t.Fatalf("machines = %+v", m)
	}
}

// A relay that ignores its own records lets a second key enroll with a used
// link. The hub's own record refuses it.
func TestUsedLinkRefusedThroughLyingRelay(t *testing.T) {
	f := start(t)
	link, l, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, fp1 := f.joinDock("a", link)
	f.eventually("first dock paired", func() bool { return f.paired(fp1) })

	// The lying relay: put the used ticket back as unused.
	j, _ := pair.ParseJoin(link)
	f.relay.AddTicket(pair.TicketHash(pair.Ticket(j.Secret)), relay.Ticket{
		Role: relay.RoleDock, Hub: f.hub.Fingerprint(), LinkID: l.ID, Name: "studio", Expires: time.Now().Add(time.Hour).UnixMilli(),
	})
	_, fp2 := f.joinDock("b", link)
	time.Sleep(3 * time.Second)
	if f.paired(fp2) {
		t.Fatal("the hub paired a second key with a used link")
	}
}

// A relay that enrolls its own key under a pending link cannot pair: it does
// not know the pairing key.
func TestSwappedKeyCannotPair(t *testing.T) {
	f := start(t)
	link, l, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	// The relay operator makes its own ticket for the same link ID and enrolls
	// its own dock with a forged link: right hub, wrong secret.
	j, _ := pair.ParseJoin(link)
	fake := pair.Join{Relay: j.Relay, Pin: j.Pin, HubFP: j.HubFP, Secret: pair.NewSecret(), Name: "studio"}
	f.relay.AddTicket(pair.TicketHash(pair.Ticket(fake.Secret)), relay.Ticket{
		Role: relay.RoleDock, Hub: f.hub.Fingerprint(), LinkID: l.ID, Name: "studio", Expires: time.Now().Add(time.Hour).UnixMilli(),
	})
	_, fpFake := f.joinDock("fake", fake.String())
	time.Sleep(3 * time.Second)
	if f.paired(fpFake) {
		t.Fatal("the hub paired a key that could not prove the link's secret")
	}
	// The real machine can still use its link afterwards.
	_, fpReal := f.joinDock("real", link)
	f.eventually("the real dock still pairs", func() bool { return f.paired(fpReal) })
}

func TestCancelledLinkRefused(t *testing.T) {
	f := start(t)
	link, l, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.hub.CancelLink(l.ID); err != nil {
		t.Fatal(err)
	}
	// The relay still has the ticket (a lying relay would not drop it).
	_, fp := f.joinDock("a", link)
	time.Sleep(3 * time.Second)
	if f.paired(fp) {
		t.Fatal("the hub paired a cancelled link")
	}
}

func TestRemoveCutsTheDock(t *testing.T) {
	f := start(t)
	link, _, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, fp := f.joinDock("a", link)
	f.eventually("paired", func() bool { return f.onlineWithInfo(fp) })
	if err := f.hub.RemoveDock(f.ctx, fp); err != nil {
		t.Fatal(err)
	}
	if f.paired(fp) {
		t.Fatal("still listed after removal")
	}
	f.eventually("the relay forgets the key", func() bool {
		_, ok := f.relay.Members()[fp]
		return !ok
	})
}

func TestPinnedKeys(t *testing.T) {
	dir := t.TempDir()
	a, err := keys.LoadOrCreate(filepath.Join(dir, "a"), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := keys.LoadOrCreate(filepath.Join(dir, "b"), "b")
	verify := keys.VerifyPinned(func(fp string) bool { return fp == a.Fingerprint })
	if err := verify(a.Cert.Certificate, nil); err != nil {
		t.Fatalf("own self-signed key rejected: %v", err)
	}
	if err := verify(b.Cert.Certificate, nil); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("other key accepted: %v", err)
	}
}

func TestProofBindsKeysAndLink(t *testing.T) {
	secret := pair.NewSecret()
	pk := pair.PairKey(secret)
	dir := t.TempDir()
	d1, _ := keys.LoadOrCreate(filepath.Join(dir, "d1"), "d")
	d2, _ := keys.LoadOrCreate(filepath.Join(dir, "d2"), "d")
	h1, _ := keys.LoadOrCreate(filepath.Join(dir, "h1"), "h")
	h2, _ := keys.LoadOrCreate(filepath.Join(dir, "h2"), "h")
	id := pair.LinkID(secret)
	proof, err := pair.Proof(pk, d1.Fingerprint, h1.Fingerprint, id)
	if err != nil {
		t.Fatal(err)
	}
	if !pair.CheckProof(pk, d1.Fingerprint, h1.Fingerprint, id, proof) {
		t.Fatal("valid proof rejected")
	}
	for name, ok := range map[string]bool{
		"other dock key": pair.CheckProof(pk, d2.Fingerprint, h1.Fingerprint, id, proof),
		"other hub key":  pair.CheckProof(pk, d1.Fingerprint, h2.Fingerprint, id, proof),
		"other link":     pair.CheckProof(pk, d1.Fingerprint, h1.Fingerprint, "x", proof),
		"other secret":   pair.CheckProof(pair.PairKey(pair.NewSecret()), d1.Fingerprint, h1.Fingerprint, id, proof),
	} {
		if ok {
			t.Errorf("proof accepted with %s", name)
		}
	}
	// the relay's ticket reveals nothing about the pairing key
	if string(pair.Ticket(secret)) == string(pk) {
		t.Fatal("ticket equals pairing key")
	}
}

func TestLinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h, _ := keys.LoadOrCreate(filepath.Join(dir, "h"), "h")
	j := pair.Join{Relay: "relay.example.com:4782", Pin: h.Fingerprint, HubFP: h.Fingerprint, Secret: pair.NewSecret(), Name: "studio mac"}
	got, err := pair.ParseJoin(j.String())
	if err != nil || got.Name != j.Name || got.Relay != j.Relay || string(got.Secret) != string(j.Secret) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := pair.ParseJoin(j.String()[:40]); err == nil {
		t.Fatal("truncated link accepted")
	}
}

func TestUploadLandsOnTheMachine(t *testing.T) {
	f := start(t)
	link, _, err := f.hub.CreateLink(f.ctx, "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, fp := f.joinDock("a", link)
	f.eventually("paired", func() bool { return f.onlineWithInfo(fp) })
	body := strings.Repeat("ember ", 50000) // 300 kB, several relay chunks
	path, _, err := f.hub.Upload(f.ctx, "test", fp, "default", "w1:p1", "../../evil name.txt", strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, dock.UploadDir()) || !strings.HasSuffix(path, "-evil_name.txt") {
		t.Fatalf("unexpected path %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body {
		t.Fatalf("file content mismatch (%v)", err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", st.Mode().Perm())
	}
}

func TestGroups(t *testing.T) {
	f := start(t)
	physics, err := f.hub.CreateGroup("Physics", "violet")
	if err != nil {
		t.Fatal(err)
	}
	coding, _ := f.hub.CreateGroup("Coding", "")
	if _, err := f.hub.CreateGroup("", ""); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := f.hub.CreateGroup("x", "neon"); err == nil {
		t.Fatal("unknown colour accepted")
	}
	dockFP := strings.Repeat("A", 43)
	agent := hub.Member{Kind: "pane", Dock: dockFP, Session: "default", Pane: "w1:p2"}
	machine := hub.Member{Kind: "dock", Dock: dockFP}
	if err := f.hub.MoveMember(agent, &physics.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.hub.MoveMember(machine, &physics.ID, ptr(0)); err != nil {
		t.Fatal(err)
	}
	// moving the agent to Coding takes it out of Physics
	if err := f.hub.MoveMember(agent, &coding.ID, nil); err != nil {
		t.Fatal(err)
	}
	gs := f.hub.Groups()
	if len(gs[0].Members) != 1 || gs[0].Members[0] != machine || len(gs[1].Members) != 1 || gs[1].Members[0] != agent {
		t.Fatalf("groups = %+v", gs)
	}
	if err := f.hub.MoveMember(hub.Member{Kind: "pane", Dock: dockFP, Session: "default", Pane: "w1;rm"}, &coding.ID, nil); err == nil {
		t.Fatal("bad pane id accepted")
	}
	if err := f.hub.ReorderGroups([]string{coding.ID, physics.ID}); err != nil {
		t.Fatal(err)
	}
	if gs := f.hub.Groups(); gs[0].ID != coding.ID {
		t.Fatal("reorder ignored")
	}
	name := "Burst"
	if _, err := f.hub.UpdateGroup(physics.ID, &name, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.hub.DeleteGroup(coding.ID); err != nil {
		t.Fatal(err)
	}
	if gs := f.hub.Groups(); len(gs) != 1 || gs[0].Name != "Burst" {
		t.Fatalf("after delete: %+v", gs)
	}
	// groups survive a hub restart
	h2, err := hub.New()
	if err != nil {
		t.Fatal(err)
	}
	if gs := h2.Groups(); len(gs) != 1 || gs[0].Name != "Burst" || len(gs[0].Members) != 1 {
		t.Fatalf("after reload: %+v", gs)
	}
}

func ptr[T any](v T) *T { return &v }
