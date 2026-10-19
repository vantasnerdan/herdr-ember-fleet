package hub

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// Member is something in a group: a whole machine user (dock) or one agent (pane).
type Member struct {
	Kind    string `json:"kind"` // "dock" or "pane"
	Dock    string `json:"dock"`
	Session string `json:"session,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

func (m Member) key() string { return m.Kind + "|" + m.Dock + "|" + m.Session + "|" + m.Pane }

// Group is a named cluster of fleet members, e.g. Physics or Coding.
type Group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Color   string   `json:"color"`
	Members []Member `json:"members"`
}

const (
	maxGroups          = 100
	maxMembersPerGroup = 1000
)

var (
	groupColors = map[string]bool{"": true, "ember": true, "amber": true, "rose": true, "teal": true, "sky": true, "violet": true, "slate": true}
	fpRE        = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	sessRE      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	paneIDRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:p[A-Za-z0-9]{1,12}$`)
)

func cleanGroupName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > 40 {
		return "", errors.New("a group name is 1-40 characters")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", errors.New("a group name can't contain control characters")
		}
	}
	return s, nil
}

func (m Member) valid() error {
	if !fpRE.MatchString(m.Dock) {
		return errors.New("bad machine")
	}
	switch m.Kind {
	case "dock":
		if m.Session != "" || m.Pane != "" {
			return errors.New("a machine member has no session or pane")
		}
	case "pane":
		if !sessRE.MatchString(m.Session) || !paneIDRE.MatchString(m.Pane) {
			return errors.New("bad session or pane")
		}
	default:
		return errors.New(`member kind must be "dock" or "pane"`)
	}
	return nil
}

// Groups returns a copy of the groups, in display order.
func (h *Hub) Groups() []Group {
	out := []Group{}
	h.store.View(func(s *State) {
		for _, g := range s.Groups {
			cp := *g
			cp.Members = append([]Member{}, g.Members...)
			out = append(out, cp)
		}
	})
	return out
}

func (h *Hub) groupsChanged() {
	h.live.Lock()
	defer h.live.Unlock()
	for w := range h.watchers {
		w.markGroups()
	}
}

// CreateGroup adds an empty group at the end.
func (h *Hub) CreateGroup(name, color string) (Group, error) {
	name, err := cleanGroupName(name)
	if err != nil {
		return Group{}, err
	}
	if !groupColors[color] {
		return Group{}, errors.New("unknown colour")
	}
	g := &Group{ID: shortID(), Name: name, Color: color, Members: []Member{}}
	err = h.store.Update(func(s *State) error {
		if len(s.Groups) >= maxGroups {
			return errors.New("too many groups")
		}
		s.Groups = append(s.Groups, g)
		return nil
	})
	if err != nil {
		return Group{}, err
	}
	h.groupsChanged()
	return *g, nil
}

// UpdateGroup renames or recolours a group.
func (h *Hub) UpdateGroup(id string, name, color *string) (Group, error) {
	var out Group
	if name != nil {
		n, err := cleanGroupName(*name)
		if err != nil {
			return out, err
		}
		name = &n
	}
	if color != nil && !groupColors[*color] {
		return out, errors.New("unknown colour")
	}
	err := h.store.Update(func(s *State) error {
		for _, g := range s.Groups {
			if g.ID == id {
				if name != nil {
					g.Name = *name
				}
				if color != nil {
					g.Color = *color
				}
				out = *g
				out.Members = append([]Member{}, g.Members...)
				return nil
			}
		}
		return errNoGroup
	})
	if err != nil {
		return out, err
	}
	h.groupsChanged()
	return out, nil
}

var errNoGroup = errors.New("no such group")

// DeleteGroup removes a group; its members simply become ungrouped.
func (h *Hub) DeleteGroup(id string) error {
	err := h.store.Update(func(s *State) error {
		for i, g := range s.Groups {
			if g.ID == id {
				s.Groups = append(s.Groups[:i], s.Groups[i+1:]...)
				return nil
			}
		}
		return errNoGroup
	})
	if err == nil {
		h.groupsChanged()
	}
	return err
}

// MoveMember puts m into group (at index, or the end), taking it out of any
// other group. A nil group just takes it out.
func (h *Hub) MoveMember(m Member, group *string, index *int) error {
	if err := m.valid(); err != nil {
		return err
	}
	err := h.store.Update(func(s *State) error {
		var target *Group
		if group != nil {
			for _, g := range s.Groups {
				if g.ID == *group {
					target = g
				}
			}
			if target == nil {
				return errNoGroup
			}
		}
		k := m.key()
		for _, g := range s.Groups {
			kept := g.Members[:0]
			for _, x := range g.Members {
				if x.key() != k {
					kept = append(kept, x)
				}
			}
			g.Members = kept
		}
		if target == nil {
			return nil
		}
		if len(target.Members) >= maxMembersPerGroup {
			return errors.New("that group is full")
		}
		at := len(target.Members)
		if index != nil && *index >= 0 && *index < at {
			at = *index
		}
		target.Members = append(target.Members, Member{})
		copy(target.Members[at+1:], target.Members[at:])
		target.Members[at] = m
		return nil
	})
	if err == nil {
		h.groupsChanged()
	}
	return err
}

// ReorderGroups sets the display order. Unknown IDs are ignored and groups
// left out keep their place at the end.
func (h *Hub) ReorderGroups(ids []string) error {
	err := h.store.Update(func(s *State) error {
		byID := map[string]*Group{}
		for _, g := range s.Groups {
			byID[g.ID] = g
		}
		var out []*Group
		for _, id := range ids {
			if g := byID[id]; g != nil {
				out = append(out, g)
				delete(byID, id)
			}
		}
		for _, g := range s.Groups {
			if byID[g.ID] != nil {
				out = append(out, g)
			}
		}
		s.Groups = out
		return nil
	})
	if err == nil {
		h.groupsChanged()
	}
	return err
}

// dropDockMembersLocked removes a removed dock (and its panes) from every group.
func dropDockMembersLocked(s *State, dock string) {
	for _, g := range s.Groups {
		kept := g.Members[:0]
		for _, x := range g.Members {
			if x.Dock != dock {
				kept = append(kept, x)
			}
		}
		g.Members = kept
	}
}

// RenameFleet changes the fleet's name.
func (h *Hub) RenameFleet(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return "", errors.New("a fleet name is 1-40 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("a fleet name can't contain control characters")
		}
	}
	if err := h.store.Update(func(s *State) error { s.Fleet = name; return nil }); err != nil {
		return "", err
	}
	h.live.Lock()
	for w := range h.watchers {
		w.markFleet()
	}
	h.live.Unlock()
	return name, nil
}

// FleetName is the fleet's display name.
func (h *Hub) FleetName() string {
	var n string
	h.store.View(func(s *State) { n = s.Fleet })
	return n
}
