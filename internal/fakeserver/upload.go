package fakeserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request, path, bundleSHA string, omitSHA bool, versionCode int64) {
	if strings.HasPrefix(path, "/upload/session/") {
		s.serveChunk(w, r, strings.TrimPrefix(path, "/upload/session/"), bundleSHA, omitSHA, versionCode)
		return
	}
	if r.URL.Query().Get("uploadType") == "resumable" {
		s.startSession(w, r)
		return
	}
	if strings.Contains(path, "/deobfuscationFiles/") {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	sum, err := hashMultipart(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": 400, "message": "cannot read upload body", "status": "INVALID_ARGUMENT"},
		})
		return
	}
	writeBundle(w, sum, bundleSHA, omitSHA, versionCode)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*bytes.Buffer{}
	}
	id := strconv.Itoa(len(s.sessions) + 1)
	s.sessions[id] = &bytes.Buffer{}
	s.mu.Unlock()
	w.Header().Set("Location", "http://"+r.Host+"/upload/session/"+id)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) serveChunk(w http.ResponseWriter, r *http.Request, id, bundleSHA string, omitSHA bool, versionCode int64) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": 400, "message": "cannot read chunk", "status": "INVALID_ARGUMENT"},
		})
		return
	}
	s.mu.Lock()
	buf := s.sessions[id]
	if buf == nil {
		s.mu.Unlock()
		notFound(w)
		return
	}
	buf.Write(body)
	final := !strings.Contains(r.Header.Get("Content-Range"), "/*")
	sum := sha256.Sum256(buf.Bytes())
	s.mu.Unlock()
	if !final {
		// google.golang.org/api v0.310.0 sends X-GUploader-No-308 and treats a
		// literal 308 as a failure. Incomplete chunks are 200 with this override.
		w.Header().Set("X-Http-Status-Code-Override", "308")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeBundle(w, hex.EncodeToString(sum[:]), bundleSHA, omitSHA, versionCode)
}

func hashMultipart(r *http.Request) (string, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var last []byte
	var media []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		buf, err := io.ReadAll(part)
		if err != nil {
			return "", err
		}
		last = append([]byte(nil), buf...)
		if strings.Contains(part.Header.Get("Content-Type"), "application/octet-stream") {
			media = last
		}
	}
	if media == nil {
		media = last
	}
	sum := sha256.Sum256(media)
	return hex.EncodeToString(sum[:]), nil
}

func writeBundle(w http.ResponseWriter, sum, override string, omit bool, versionCode int64) {
	if versionCode == 0 {
		versionCode = 42
	}
	if override != "" {
		sum = override
	}
	body := map[string]any{"versionCode": versionCode}
	if !omit {
		body["sha256"] = sum
	}
	writeJSON(w, http.StatusOK, body)
}
