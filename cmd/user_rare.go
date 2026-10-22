// Rare user-scoped surfaces from plan §5.12 / §5.13 / §5.14.
//
// Deleted commands (kept here as comments for the git history to show):
//   - locker    — removed; the CBE tenant returns an empty locker for
//     students, so the command had no real value.
//   - ePortfolio — removed because the CBE tenant disables the eP product
//     for students (every /d2l/api/eP/<v>/ call returns 403).
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/dupdata"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
	"github.com/IamCoder18/schooltools/internal/ua"
)

// ───────────────────────────── award ─────────────────────────────

var (
	awardEnvFile string
	awardAutoRef bool
	awardJSON    bool
)

type Award struct {
	Id            string `json:"Id"`
	Title         string `json:"Title"`
	Description   string `json:"Description"`
	IssuedDate    string `json:"IssuedDate"`
	ExpiryDate    string `json:"ExpiryDate"`
	AwardType     string `json:"AwardType"`
	CertificateId string `json:"CertificateId"`
	OrgUnitId     int    `json:"OrgUnitId"`
	OrgUnitName   string `json:"OrgUnitName"`
}

func init() {
	root := &cobra.Command{
		Use:   "award",
		Short: "Issued badges and certificates (rare)",
		Long:  "List your issued awards. `award get <id>` shows one; `award download <certificateId>` saves the PDF.",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runAwardList() },
	}
	root.Flags().StringVarP(&awardEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&awardAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&awardJSON, "json", false, "Emit raw JSON")

	get := &cobra.Command{
		Use: "get <id>", Short: "Show one issued award", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAwardGet(args[0]) },
	}
	get.Flags().StringVarP(&awardEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	get.Flags().BoolVar(&awardAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	get.Flags().BoolVar(&awardJSON, "json", false, "Emit raw JSON")

	dl := &cobra.Command{
		Use: "download <certificateId>", Short: "Download a certificate PDF", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAwardDownload(args[0]) },
	}
	dl.Flags().StringVar(&asgOut, "out", defaultHome(), "Output directory")
	dl.Flags().StringVar(&asgName, "name", "", "Override output filename")
	dl.Flags().StringVarP(&awardEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	dl.Flags().BoolVar(&awardAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")

	root.AddCommand(get, dl)
	rootCmd.AddCommand(root)
}

func awardSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     awardEnvFile,
		AutoRefresh: awardAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func runAwardList() error {
	ens, err := awardSession()
	if err != nil {
		return err
	}
	uid, err := dupdata.CurrentUserID(ens.Jar)
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/bas/%s/issued/users/%s/", ua.D2LBase, dupdata.BASVersion(), uid)
	items, err := dupdata.FetchJSONList[Award](raw, ens.Jar)
	if err != nil {
		return err
	}
	if awardJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No awards issued to you."))
		return nil
	}
	fmt.Print(render.Title("Awards"))
	for _, a := range items {
		fmt.Print(awardPanel(a))
	}
	fmt.Print(render.Hint("schooltools award get <id>   for one award in full"))
	return nil
}

func runAwardGet(id string) error {
	ens, err := awardSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/bas/%s/issued/%s", ua.D2LBase, dupdata.BASVersion(), id)
	var out Award
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	if awardJSON {
		return render.PrintJSON(out)
	}
	var b strings.Builder
	b.WriteString(render.Title(out.Title))
	rows := append([]render.Row{{Key: "Award", Value: out.Id}}, awardRows(out)...)
	if out.CertificateId != "" {
		rows = append(rows, render.Row{Key: "Certificate", Value: out.CertificateId})
	}
	b.WriteString(render.KV(rows...))
	b.WriteString(awardDescription(out))
	if out.CertificateId != "" {
		b.WriteString(render.Hint("schooltools award download %s", out.CertificateId))
	}
	fmt.Print(b.String())
	return nil
}

func runAwardDownload(certID string) error {
	ens, err := awardSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/bas/%s/issued/certificates/%s/pdf",
		ua.D2LBase, dupdata.BASVersion(), certID)
	return asgDownloadFile(ens.Jar, raw, "certificate-"+certID+".pdf")
}

func awardCourseLabel(a Award) string {
	if a.OrgUnitName != "" {
		return a.OrgUnitName
	}
	if a.OrgUnitId != 0 {
		return fmt.Sprintf("org %d", a.OrgUnitId)
	}
	return ""
}

func awardRows(a Award) []render.Row {
	rows := []render.Row{}
	if a.AwardType != "" {
		rows = append(rows, render.Row{Key: "Type", Value: a.AwardType})
	}
	rows = append(rows, render.Row{Key: "Issued", Value: render.Date(a.IssuedDate)})
	if a.ExpiryDate != "" {
		rows = append(rows, render.Row{Key: "Expires", Value: render.Date(a.ExpiryDate)})
	}
	if c := awardCourseLabel(a); c != "" {
		rows = append(rows, render.Row{Key: "Course", Value: c})
	}
	return rows
}

func awardDescription(a Award) string {
	d := strings.TrimSpace(htmlToText(a.Description))
	if d == "" {
		return ""
	}
	return render.Section("Description") + render.Wrap(d, render.Width()) + "\n"
}

func awardPanel(a Award) string {
	var b strings.Builder
	b.WriteString(render.Section(a.Title))
	rows := awardRows(a)
	if a.CertificateId != "" {
		rows = append(rows, render.Row{Key: "PDF", Value: "schooltools award download " + a.CertificateId})
	}
	b.WriteString(render.KV(rows...))
	b.WriteString(awardDescription(a))
	return b.String()
}
