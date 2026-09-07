package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/content"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

var (
	downloadOut         string
	downloadName        string
	downloadJSON        bool
	downloadEnvFile     string
	downloadAutoRefr    bool
	downloadNoOverwrite bool
	downloadCourse      string
)

// downloadCmd is the new (0.2.0) form: `download --course <id> <topicId>`.
// The legacy positional form `download <courseId>/topics/<topicId>` is
// detected inside RunE and prints a deprecation notice.
var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download a D2L topic's file (or its metadata JSON) — scope with --course",
	Long: "Fetch a single D2L topic and save it under your home directory. The\n" +
		"course is selected by `--course <orgUnitId>`; the positional argument\n" +
		"is the topicId alone.\n\n" +
		"For a File topic, the underlying file is downloaded with its\n" +
		"server-reported filename. For a Link or hosted-HTML topic, the topic\n" +
		"metadata JSON is written instead and the link URL is printed.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Legacy form: `download <courseId>/topics/<topicId>` etc.
		if len(args) == 1 && downloadCourse == "" {
			// Looks like a URL-shaped reference that contains a '/' — treat
			// as the legacy form.
			if strings.ContainsAny(args[0], "/") || strings.Contains(args[0], "topics") {
				fmt.Fprintln(os.Stderr, "Deprecated: 'schooltools download <ref>' is now 'schooltools download --course <id> <topicId>'.")
				fmt.Fprintln(os.Stderr, "This alias will be removed in a future release.")
				courseID, topicID, err := content.ParseTopicRef(args[0])
				if err != nil {
					return err
				}
				return runDownloadForCourse(courseID, topicID)
			}
		}
		if len(args) == 0 {
			return fmt.Errorf("expected a topicId; pass --course <orgUnitId> and a topicId positional")
		}
		courseID, err := requireDownloadCourse()
		if err != nil {
			return err
		}
		tid, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || tid <= 0 {
			return fmt.Errorf("invalid topicId %q: expected a positive integer", args[0])
		}
		return runDownloadForCourse(courseID, tid)
	},
}

func init() {
	downloadCmd.Flags().StringVar(&downloadOut, "out", defaultHome(), "Directory to write the downloaded file into (default: $HOME)")
	downloadCmd.Flags().StringVar(&downloadName, "name", "", "Override the output filename (default: inferred from the topic)")
	downloadCmd.Flags().BoolVar(&downloadJSON, "json", false, "Print the topic metadata JSON to stdout instead of downloading the file")
	downloadCmd.Flags().BoolVar(&downloadNoOverwrite, "no-clobber", false, "Refuse to overwrite an existing file at the destination")
	downloadCmd.Flags().StringVarP(&downloadEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	downloadCmd.Flags().BoolVar(&downloadAutoRefr, "auto-refresh", false, "Re-run 'login' if the session is expired")
	downloadCmd.Flags().StringVar(&downloadCourse, "course", "", "Course OrgUnitId the topic belongs to (required)")
	rootCmd.AddCommand(downloadCmd)
}

func requireDownloadCourse() (string, error) {
	if downloadCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return downloadCourse, nil
}

func runDownloadForCourse(courseID string, topicID int) error {
	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     downloadEnvFile,
		AutoRefresh: downloadAutoRefr,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}

	topic, err := fetchTopic(ens.Jar, courseID, topicID)
	if err != nil {
		if isLoginError(err) {
			_ = session.Clear()
			return fmt.Errorf("session expired; run 'schooltools login' again")
		}
		return fmt.Errorf("fetch topic metadata: %w", err)
	}

	if downloadJSON {
		out, mErr := json.MarshalIndent(topic, "", "  ")
		if mErr != nil {
			return mErr
		}
		fmt.Println(string(out))
		return nil
	}

	if !topic.IsFile() {
		return writeTopicMetadata(courseID, topic)
	}

	fileURL := topic.FileURL()
	if fileURL == "" {
		return fmt.Errorf("topic %d is a File but has no Url; nothing to download", topic.TopicId)
	}

	return fetchAndSaveFile(ens.Jar, courseID, topic, fileURL)
}

func defaultHome() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "."
	}
	return h
}

func fetchTopic(jar *cookiejar.Jar, courseID string, topicID int) (content.Topic, error) {
	return content.FetchTopic(courseID, topicID, jar)
}

func writeTopicMetadata(courseID string, t content.Topic) error {
	outDir, err := filepath.Abs(downloadOut)
	if err != nil {
		return fmt.Errorf("resolve out dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	name := downloadName
	if name == "" {
		name = fmt.Sprintf("topic-%s-%d.json", courseID, t.TopicId)
	}
	name = sanitizeFilename(name)
	path := filepath.Join(outDir, name)

	exists, err := pathExists(path)
	if err != nil {
		return err
	}
	if exists && downloadNoOverwrite {
		return fmt.Errorf("refusing to overwrite existing file: %s", path)
	}

	body, _ := json.MarshalIndent(t, "", "  ")
	if exists {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	switch {
	case t.IsLink():
		fmt.Printf("Topic %d is a Link; saved metadata to %s\n", t.TopicId, path)
		if u := t.FileURL(); u != "" {
			fmt.Printf("Link URL: %s\n", u)
		}
	default:
		fmt.Printf("Topic %d (type=%q) has no document body to download; saved metadata to %s\n",
			t.TopicId, t.TypeIdentifier, path)
	}
	return nil
}

func fetchAndSaveFile(jar *cookiejar.Jar, courseID string, t content.Topic, fileURL string) error {
	resolved, err := resolveFileURL(fileURL)
	if err != nil {
		return fmt.Errorf("resolve file url %q: %w", fileURL, err)
	}
	res, err := httpclient.FollowRedirects(resolved, jar, httpclient.FetchOptions{})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", resolved, err)
	}
	if res.StatusCode/100 != 2 {
		_ = res.Body.Close()
		return fmt.Errorf("download returned %d %s for topic %d", res.StatusCode, res.Status, t.TopicId)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	outDir, err := filepath.Abs(downloadOut)
	if err != nil {
		return fmt.Errorf("resolve out dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}

	name := downloadName
	if name == "" {
		name = inferFilename(t, resolved, res.Header.Get("Content-Disposition"))
	}
	name = sanitizeFilename(name)
	if name == "" {
		name = fmt.Sprintf("topic-%s-%d", courseID, t.TopicId)
	}
	path := filepath.Join(outDir, name)

	exists, err := pathExists(path)
	if err != nil {
		return err
	}
	if exists && downloadNoOverwrite {
		return fmt.Errorf("refusing to overwrite existing file: %s", path)
	}
	if exists {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("Downloaded: %s (%s)\n", path, humanBytes(int64(len(body))))
	if u, perr := url.Parse(resolved); perr == nil {
		fmt.Printf("  course:   %s\n", courseID)
		fmt.Printf("  topic:    %d — %s\n", t.TopicId, strings.TrimSpace(t.Title))
		fmt.Printf("  source:   %s\n", u.Path)
	}
	return nil
}

func inferFilename(t content.Topic, rawURL, contentDisposition string) string {
	if t.FileName != "" {
		return t.FileName
	}
	if name := filenameFromContentDisposition(contentDisposition); name != "" {
		return name
	}
	if name := basenameFromURL(rawURL); name != "" {
		return name
	}
	return fmt.Sprintf("topic-%d", t.TopicId)
}

// filenameFromContentDisposition pulls `filename=` (or filename*) out of a
// Content-Disposition header. Returns "" if nothing parseable is present.
func filenameFromContentDisposition(h string) string {
	if h == "" {
		return ""
	}
	for _, part := range strings.Split(h, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if !strings.HasPrefix(lower, "filename=") {
			continue
		}
		v := strings.TrimSpace(part[len("filename="):])
		v = strings.Trim(v, `"`)
		if v != "" {
			return v
		}
	}
	return ""
}

func basenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return ""
	}
	base := filepath.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	r := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_")
	name = r.Replace(name)
	name = strings.Trim(name, " .")
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func resolveFileURL(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return rawURL, nil
	}
	if u.Scheme == "" && u.Host == "" && (strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, "./") || strings.HasPrefix(rawURL, "../")) {
		base, perr := url.Parse(ua.D2LBase)
		if perr != nil {
			return "", perr
		}
		return base.ResolveReference(u).String(), nil
	}
	return rawURL, nil
}

func pathExists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
