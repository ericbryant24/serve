package server

import (
	"io"
	"net/http"
	"os"
	"strings"

	"serve/internal/logx"
	"serve/internal/reports"
)

// Reports are captured in the browser and stay on this machine. The review
// step shows exactly what an export will contain; Export downloads it.

type attachmentReview struct {
	reports.Attachment
	Text    string           `json:"text,omitempty"`
	Secrets []logx.SecretHit `json:"secrets,omitempty"`
}

type reportReview struct {
	Report      *reports.Report    `json:"report"`
	Title       string             `json:"title"`
	Markdown    string             `json:"markdown"`
	BodySecrets []logx.SecretHit   `json:"body_secrets,omitempty"`
	Attachments []attachmentReview `json:"attachments"`
	SecretCount int                `json:"secret_count"`
	Dir         string             `json:"dir"`
}

func (s *Server) review(r *reports.Report) reportReview {
	dir, _ := s.reports.Dir(r.ID)
	rv := reportReview{Report: r, Title: r.DisplayTitle(), Markdown: r.Markdown(), BodySecrets: logx.ScanSecrets(r.Title + "\n" + r.Body), Attachments: []attachmentReview{}, Dir: dir}
	rv.SecretCount = len(rv.BodySecrets)
	for _, a := range r.Attachments {
		ar := attachmentReview{Attachment: a}
		if a.Kind == reports.AttachLog || a.Kind == reports.AttachRepro {
			if p, err := s.reports.AttachmentPath(r.ID, a.ID); err == nil {
				if data, err := os.ReadFile(p); err == nil {
					ar.Text = string(data)
					ar.Secrets = logx.ScanSecrets(ar.Text)
					if a.Included {
						rv.SecretCount += len(ar.Secrets)
					}
				}
			}
		}
		rv.Attachments = append(rv.Attachments, ar)
	}
	return rv
}

func (s *Server) registerReportAPI(mux *http.ServeMux, owner func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /_serve/api/reports", owner(func(w http.ResponseWriter, r *http.Request) {
		list, err := s.reports.List()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"reports": list})
	}))
	mux.HandleFunc("POST /_serve/api/reports", owner(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind     string `json:"kind"`
			Title    string `json:"title"`
			Body     string `json:"body"`
			Browser  string `json:"browser"`
			ViewKind string `json:"view_kind"`
			Repro    string `json:"repro"`
			WithLog  bool   `json:"with_log"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, 400, "bad request")
			return
		}
		if strings.TrimSpace(in.Title) == "" {
			writeError(w, 400, "a title is required")
			return
		}
		rep, err := s.reports.Create(reports.Report{Kind: in.Kind, Title: in.Title, Body: in.Body, Env: reports.Env{Browser: in.Browser, ViewKind: in.ViewKind}})
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if strings.TrimSpace(in.Repro) != "" {
			_, _ = s.reports.AddAttachment(rep.ID, reports.AttachRepro, "", "text/markdown", []byte(in.Repro))
		}
		if in.WithLog {
			_, _ = s.reports.AddAttachment(rep.ID, reports.AttachLog, "", "text/plain", []byte(logx.Snapshot()))
		}
		rep, _ = s.reports.Get(rep.ID)
		logx.Logf("info", "report captured", logx.Safe("kind", rep.Kind))
		writeJSON(w, 201, s.review(rep))
	}))
	mux.HandleFunc("GET /_serve/api/reports/{id}", owner(func(w http.ResponseWriter, r *http.Request) {
		rep, err := s.reports.Get(r.PathValue("id"))
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, 200, s.review(rep))
	}))
	mux.HandleFunc("PATCH /_serve/api/reports/{id}", owner(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title    *string `json:"title"`
			Body     *string `json:"body"`
			Include  *string `json:"include"`
			Included *bool   `json:"included"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, 400, "bad request")
			return
		}
		rep, err := s.reports.Update(r.PathValue("id"), func(rep *reports.Report) error {
			if in.Title != nil {
				rep.Title = *in.Title
			}
			if in.Body != nil {
				rep.Body = *in.Body
			}
			if in.Include != nil && in.Included != nil {
				for i := range rep.Attachments {
					if rep.Attachments[i].ID == *in.Include {
						rep.Attachments[i].Included = *in.Included
					}
				}
			}
			return nil
		})
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, 200, s.review(rep))
	}))
	mux.HandleFunc("DELETE /_serve/api/reports/{id}", owner(func(w http.ResponseWriter, r *http.Request) {
		if err := s.reports.Delete(r.PathValue("id")); err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /_serve/api/reports/{id}/attachments", owner(func(w http.ResponseWriter, r *http.Request) {
		const limit = 12 << 20
		if err := r.ParseMultipartForm(limit); err != nil {
			writeError(w, 400, "could not read the upload")
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "no file in the upload")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		if err != nil || len(data) > limit {
			writeError(w, 413, "the capture is too large")
			return
		}
		kind := r.FormValue("kind")
		if kind == "" {
			kind = reports.AttachScreenshot
		}
		mime := hdr.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		if _, err := s.reports.AddAttachment(r.PathValue("id"), kind, r.FormValue("mode"), mime, data); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		rep, _ := s.reports.Get(r.PathValue("id"))
		writeJSON(w, 200, s.review(rep))
	}))
	mux.HandleFunc("GET /_serve/api/reports/{id}/attachments/{aid}", owner(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.reports.AttachmentPath(r.PathValue("id"), r.PathValue("aid"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, p)
	}))
	mux.HandleFunc("GET /_serve/api/reports/{id}/export", owner(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := s.reports.Get(id); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="serve-report-`+id+`.zip"`)
		if err := s.reports.Export(id, w); err != nil {
			logx.Error("report export failed", err)
		}
	}))
	mux.HandleFunc("POST /_serve/api/reports/{id}/reveal", owner(func(w http.ResponseWriter, r *http.Request) {
		dir, err := s.reports.Dir(r.PathValue("id"))
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		if err := reveal(dir); err != nil {
			writeError(w, 501, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
}
