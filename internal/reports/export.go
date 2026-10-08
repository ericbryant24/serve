package reports

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Export writes a report as a zip: report.md (the text the reporter saw in
// the review step) plus the attachments they chose to include, and nothing
// else.
func (s *ReportStore) Export(id string, w io.Writer) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		h := &zip.FileHeader{Name: "serve-report-" + id + "/" + name, Method: zip.Deflate, Modified: time.Now()}
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	md := "# " + r.DisplayTitle() + "\n\n" + r.Markdown()
	if err := add("report.md", []byte(md)); err != nil {
		return err
	}
	for _, a := range r.IncludedAttachments() {
		data, err := os.ReadFile(filepath.Join(s.reportDir(id), filepath.Base(a.File)))
		if err != nil {
			return err
		}
		if err := add(filepath.Base(a.File), data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// ExportFile writes the zip to path.
func (s *ReportStore) ExportFile(id, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := s.Export(id, f); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
