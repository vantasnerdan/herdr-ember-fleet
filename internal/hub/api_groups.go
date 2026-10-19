package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
)

const maxUpload = 50 << 20

func (w *Web) renameFleet(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name string `json:"name"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	name, err := w.h.RenameFleet(req.Name)
	if err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "name": name})
}

func (w *Web) listGroups(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, map[string]any{"groups": w.h.Groups()})
}

func (w *Web) createGroup(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	g, err := w.h.CreateGroup(req.Name, req.Color)
	if err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "group": g})
}

func (w *Web) updateGroup(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	g, err := w.h.UpdateGroup(r.PathValue("id"), req.Name, req.Color)
	if err != nil {
		jsonErr(rw, groupErrCode(err), err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "group": g})
}

func (w *Web) deleteGroup(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.DeleteGroup(r.PathValue("id")); err != nil {
		jsonErr(rw, groupErrCode(err), err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) moveMember(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Member Member  `json:"member"`
		Group  *string `json:"group"`
		Index  *int    `json:"index"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	if err := w.h.MoveMember(req.Member, req.Group, req.Index); err != nil {
		jsonErr(rw, groupErrCode(err), err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "groups": w.h.Groups()})
}

func (w *Web) reorderGroups(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if readJSON(r, &req) != nil || len(req.IDs) > maxGroups*2 {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	if err := w.h.ReorderGroups(req.IDs); err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "groups": w.h.Groups()})
}

func groupErrCode(err error) int {
	if errors.Is(err, errNoGroup) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// upload streams one file to the agent's machine over an end-to-end "upload"
// channel. The dock saves it under that user's ~/.cache/ember-fleet/uploads
// and answers with the absolute path, which the page puts into the prompt.
func (w *Web) upload(rw http.ResponseWriter, r *http.Request, p principal) {
	q := r.URL.Query()
	if r.ContentLength <= 0 {
		jsonErr(rw, http.StatusLengthRequired, "the upload needs a Content-Length")
		return
	}
	if r.ContentLength > maxUpload {
		jsonErr(rw, http.StatusRequestEntityTooLarge, "files are limited to 50 MB")
		return
	}
	name, err := url.QueryUnescape(r.Header.Get("X-Ember-Filename"))
	if err != nil || name == "" || len(name) > 255 {
		name = "file"
	}
	path, code, err := w.h.Upload(r.Context(), p.name, q.Get("dock"), q.Get("session"), q.Get("pane"), name, http.MaxBytesReader(rw, r.Body, maxUpload), r.ContentLength)
	if err != nil {
		jsonErr(rw, code, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "path": path, "name": name, "size": r.ContentLength})
}

// Upload sends size bytes from body to a dock, which saves them as a file
// for that user and returns the absolute path. The int is an HTTP status for
// errors.
func (h *Hub) Upload(ctx context.Context, operator, dock, session, pane, name string, body io.Reader, size int64) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	hdr := e2e.Header{Kind: e2e.KindUpload, Operator: operator, Session: session, Pane: pane, Name: name, Size: size}
	t, br, rep, cut, err := h.openPaired(ctx, dock, hdr)
	if err != nil {
		return "", http.StatusBadGateway, err
	}
	defer cut()
	defer t.Close()
	if !rep.OK {
		if rep.Code == "forbidden" {
			return "", http.StatusForbidden, errors.New(rep.Error)
		}
		return "", http.StatusBadRequest, errors.New(rep.Error)
	}
	n, err := io.Copy(t, io.LimitReader(body, size))
	if err != nil || n != size {
		return "", http.StatusBadRequest, errors.New("the upload was cut short")
	}
	var done e2e.Reply
	if err := e2e.ReadJSON(br, &done); err != nil {
		return "", http.StatusBadGateway, errors.New("the machine did not confirm the upload")
	}
	if !done.OK {
		return "", http.StatusBadGateway, errors.New(done.Error)
	}
	return done.Path, 0, nil
}

func (w *Web) getUpdates(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, w.h.Updates())
}

func (w *Web) setUpdates(rw http.ResponseWriter, r *http.Request, p principal) {
	u := w.h.updateSettings()
	if readJSON(r, &u) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	if err := w.h.SetUpdateSettings(u); err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	w.h.updatesChanged()
	jsonOut(rw, w.h.Updates())
}

func (w *Web) runUpdates(rw http.ResponseWriter, r *http.Request, p principal) {
	if !w.h.RunUpdates(context.WithoutCancel(r.Context()), "button") {
		jsonErr(rw, http.StatusConflict, "an update run is already going")
		return
	}
	jsonOut(rw, w.h.Updates())
}
