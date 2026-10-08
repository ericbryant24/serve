package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"serve/internal/reports"
)

const reportUsage = `Usage: serve report [list|show|export|open|rm] ...

Bug reports and feature requests captured with "Report a problem" in the
browser. They stay on this machine until you send one yourself.

  serve report                      list reports (--json for scripting)
  serve report show <id>            print one report as JSON
  serve report export <id> [-o F]   write it as a zip (report.md plus the
                                    attachments you included) to send
  serve report export <id> --markdown   print just the report text
  serve report open <id>            open the report's folder
  serve report rm <id>...           delete reports`

func cmdReport(args []string) error {
	sub := "list"
	if len(args) > 0 && args[0] != "--json" && args[0] != "-h" && args[0] != "--help" {
		sub, args = args[0], args[1:]
	}
	st := reports.NewStore()
	switch sub {
	case "list", "ls":
		fs := flags("report", reportUsage)
		asJSON := fs.Bool("json", false, "")
		if _, err := parse(fs, reportUsage, args); err != nil {
			return err
		}
		list, err := st.List()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(list)
		}
		if len(list) == 0 {
			fmt.Println(`No reports yet. Use "Report a problem" in the browser to capture one.`)
			return nil
		}
		for _, r := range list {
			date := r.CreatedAt
			if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
				date = t.Local().Format("2006-01-02 15:04")
			}
			fmt.Printf("%-12s  %-7s  %-16s  %s\n", r.ID, r.Kind, date, oneLine(r.Title, 60))
		}
		return nil
	case "show":
		if len(args) != 1 {
			return usageError{reportUsage}
		}
		r, err := st.Get(args[0])
		if err != nil {
			return fmt.Errorf("no report %q", args[0])
		}
		return printJSON(r)
	case "export":
		fs := flags("report export", reportUsage)
		out := fs.String("o", "", "")
		md := fs.Bool("markdown", false, "")
		pos, err := parse(fs, reportUsage, args)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError{reportUsage}
		}
		r, err := st.Get(pos[0])
		if err != nil {
			return fmt.Errorf("no report %q", pos[0])
		}
		if *md {
			fmt.Printf("# %s\n\n%s\n", r.DisplayTitle(), r.Markdown())
			return nil
		}
		if *out == "" {
			*out = "serve-report-" + r.ID + ".zip"
		}
		if err := st.ExportFile(r.ID, *out); err != nil {
			return err
		}
		fmt.Println("Wrote", *out)
		return nil
	case "open":
		if len(args) != 1 {
			return usageError{reportUsage}
		}
		dir, err := st.Dir(args[0])
		if err != nil {
			return err
		}
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("no report %q", args[0])
		}
		if err := openFolder(dir); err != nil {
			fmt.Println(dir)
		}
		return nil
	case "rm", "delete":
		if len(args) == 0 {
			return usageError{reportUsage}
		}
		failed := false
		for _, id := range args {
			if err := st.Delete(id); err != nil {
				fmt.Fprintf(os.Stderr, "serve: no report %q\n", id)
				failed = true
				continue
			}
			fmt.Println("Deleted", id)
		}
		if failed {
			return errQuiet
		}
		return nil
	case "-h", "--help", "help":
		fmt.Println(reportUsage)
		return nil
	}
	return usageError{reportUsage}
}

func openFolder(dir string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", dir).Run()
	case "windows":
		return exec.Command("explorer", dir).Run()
	case "linux":
		return exec.Command("xdg-open", dir).Run()
	}
	return errors.New("unsupported")
}
