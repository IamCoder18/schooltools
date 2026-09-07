// Rare user-scoped surfaces from plan §5.12 / §5.13 / §5.14.
//
// Deleted commands (kept here as comments for the git history to show):
//   - locker    — removed; the CBE tenant returns an empty locker for
//                 students, so the command had no real value.
//   - ePortfolio — removed because the CBE tenant disables the eP product
//                 for students (every /d2l/api/eP/<v>/ call returns 403).
package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No awards issued to you.")
		return nil
	}
	fmt.Printf("%d award%s\n", len(items), plural(len(items)))
	for _, a := range items {
		when := a.IssuedDate
		if len(when) > 16 {
			when = when[:16]
		}
		fmt.Printf("  %s\t%s\t%s\n", a.Id, when, a.Title)
	}
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
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
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

// suppress unused-import warnings for imports used only by sibling files.
var _ = strconv.Itoa
var _ = url.Parse
var _ = strings.HasPrefix
