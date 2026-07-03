package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxImportFileSize = 50 * 1024 * 1024 // 50 MB

// ---------------------------------------------------------------------------
// POST /api/sessions/{sid}/import  (multipart/form-data, field "file": json|zip)
// ---------------------------------------------------------------------------

func (s *server) doImportSession(sess *Session, w http.ResponseWriter, r *http.Request) {
	sid := sess.id
	if st, ok := s.sessions.getImportStatus(sid); ok && st.Status == "importing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "import already in progress"})
		return
	}

	if err := r.ParseMultipartForm(maxImportFileSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form: " + err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing 'file' field: " + err.Error()})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxImportFileSize))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reading file: " + err.Error()})
		return
	}

	s.sessions.setImportStatus(sid, importStatus{Status: "importing", Message: "Detecting format..."})

	creds, err := parseImportFile(data, header.Filename)
	if err != nil {
		s.sessions.setImportStatus(sid, importStatus{Status: "error", Message: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parse error: " + err.Error()})
		return
	}

	s.finishImport(sess, w, r, creds)
}

// ---------------------------------------------------------------------------
// POST /api/sessions/{sid}/importRaw
// Body: { "creds": {...}, "keys": {...} } (Baileys) or a raw wa-web dump
// ---------------------------------------------------------------------------

func (s *server) doImportRawSession(sess *Session, w http.ResponseWriter, r *http.Request) {
	sid := sess.id
	if st, ok := s.sessions.getImportStatus(sid); ok && st.Status == "importing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "import already in progress"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportFileSize))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reading body: " + err.Error()})
		return
	}

	s.sessions.setImportStatus(sid, importStatus{Status: "importing", Message: "Converting credentials..."})

	var creds *SessionCredentials
	if detectFormat(body) == formatWaWeb {
		creds, err = convertWaWebDump(body)
	} else {
		var req struct {
			Creds json.RawMessage            `json:"creds"`
			Keys  map[string]json.RawMessage `json:"keys"`
		}
		if uerr := json.Unmarshal(body, &req); uerr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + uerr.Error()})
			return
		}
		if len(req.Creds) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing 'creds' field (or unrecognized format)"})
			return
		}
		if detectFormat(req.Creds) == formatWaWeb {
			creds, err = convertWaWebDump(req.Creds)
		} else {
			creds, err = convertBaileysJSON(req.Creds, req.Keys)
		}
	}
	if err != nil {
		s.sessions.setImportStatus(sid, importStatus{Status: "error", Message: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversion error: " + err.Error()})
		return
	}

	s.finishImport(sess, w, r, creds)
}

// finishImport validates and applies credentials common to both import routes.
func (s *server) finishImport(sess *Session, w http.ResponseWriter, r *http.Request, creds *SessionCredentials) {
	sid := sess.id
	if err := validateCredentials(creds); err != nil {
		s.sessions.setImportStatus(sid, importStatus{Status: "error", Message: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credentials: " + err.Error()})
		return
	}

	s.sessions.setImportStatus(sid, importStatus{Status: "importing", Message: "Writing session..."})

	if err := s.sessions.ImportCredentials(r.Context(), sid, creds); err != nil {
		s.sessions.setImportStatus(sid, importStatus{Status: "error", Message: err.Error()})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.sessions.setImportStatus(sid, importStatus{Status: "connecting", Message: "Session written. Connecting...", JID: creds.JID})
	writeJSON(w, http.StatusOK, map[string]string{
		"jid":     creds.JID,
		"message": "Session imported successfully. Connection starting.",
	})
}

// ---------------------------------------------------------------------------
// GET /api/sessions/{sid}/export?format=json|zip
// ---------------------------------------------------------------------------

func (s *server) doExportSession(sess *Session, w http.ResponseWriter, r *http.Request) {
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session has no JID"})
		return
	}
	jid := sess.client.Store.ID.String()

	row, err := readDeviceCreds(s.sessions.store.DB(), jid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	credsJSON, err := json.MarshalIndent(buildBaileysExport(jid, row), "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	format := r.URL.Query().Get("format")
	switch format {
	case "zip":
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, err := zw.Create("creds.json")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if _, err := f.Write(credsJSON); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := zw.Close(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="session-%s.zip"`, sess.id))
		_, _ = w.Write(buf.Bytes())
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="creds-%s.json"`, sess.id))
		_, _ = w.Write(credsJSON)
	}
}

// ---------------------------------------------------------------------------
// GET /api/sessions/{sid}/importStatus
// ---------------------------------------------------------------------------

func (s *server) doImportStatus(sess *Session, w http.ResponseWriter, r *http.Request) {
	connStatus := "disconnected"
	jid := ""
	if sess.client.Store.ID != nil {
		jid = sess.client.Store.ID.String()
		if sess.client.IsConnected() {
			connStatus = "connected"
		} else {
			connStatus = "connecting"
		}
	}

	impSt, _ := s.sessions.getImportStatus(sess.id)
	writeJSON(w, http.StatusOK, map[string]any{
		"jid":              jid,
		"connectionStatus": connStatus,
		"importStatus":     impSt.Status,
		"importMessage":    impSt.Message,
	})
}
