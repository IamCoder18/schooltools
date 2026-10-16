package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type VerifyIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	CourseID string `json:"courseId,omitempty"`
	TopicID  int    `json:"topicId,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type VerifyResult struct {
	Root          string        `json:"root"`
	OK            bool          `json:"ok"`
	Deep          bool          `json:"deep"`
	CheckedTopics int           `json:"checkedTopics"`
	CheckedBlobs  int           `json:"checkedBlobs"`
	Issues        []VerifyIssue `json:"issues"`
	Orphans       []string      `json:"orphans"`
}

func Verify(root string, deep bool) (VerifyResult, error) {
	res := VerifyResult{Root: root, Deep: deep}

	idx, err := LoadIndex(root)
	if err != nil {
		return res, err
	}
	if idx.Version > SchemaVersion {
		res.Issues = append(res.Issues, VerifyIssue{
			Code:    "schema-version-newer",
			Message: fmt.Sprintf("store schema version %d is newer than this binary understands (%d); upgrade schooltools", idx.Version, SchemaVersion),
		})
	} else if idx.Version < SchemaVersion {
		res.Issues = append(res.Issues, VerifyIssue{
			Code:    "schema-version-older",
			Message: fmt.Sprintf("store schema version %d is older than this binary (%d); an `archive prune` will rewrite indexes to v%d", idx.Version, SchemaVersion, SchemaVersion),
		})
	}

	blobsDir := filepath.Join(root, blobsDirName)
	seenBlobs := map[string]struct{}{}

	// checkBlob distinguishes "absent" from "present but unreadable": a
	// stat failure that is not not-existence is reported as an issue
	// instead of being passed as a present blob.
	checkBlob := func(path, name, missingCode, unreadableCode, missingMsg, courseID string, topicID int) bool {
		present, err := statOK(path)
		if err != nil {
			res.Issues = append(res.Issues, VerifyIssue{
				Code:     unreadableCode,
				CourseID: courseID, TopicID: topicID, Blob: name,
				Message: fmt.Sprintf("cannot stat %s: %v", name, err),
			})
			return false
		}
		if !present {
			res.Issues = append(res.Issues, VerifyIssue{
				Code:     missingCode,
				CourseID: courseID, TopicID: topicID, Blob: name,
				Message: missingMsg,
			})
		}
		return present
	}

	courseIDs := make([]string, 0, len(idx.Courses))
	for cid := range idx.Courses {
		courseIDs = append(courseIDs, cid)
	}
	if entries, err := os.ReadDir(filepath.Join(root, coursesDirName)); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			found := false
			for _, c := range courseIDs {
				if c == e.Name() {
					found = true
					break
				}
			}
			if !found {
				courseIDs = append(courseIDs, e.Name())
			}
		}
	}
	sort.Strings(courseIDs)

	for _, cid := range courseIDs {
		dir := filepath.Join(root, coursesDirName, safeID(cid))
		if _, err := os.Stat(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				res.Issues = append(res.Issues, VerifyIssue{
					Code:     "missing-course-dir",
					CourseID: cid,
					Message:  fmt.Sprintf("global index references course %s but %s does not exist", cid, dir),
				})
			} else {
				return res, err
			}
			continue
		}
		ci, err := LoadCourseIndex(root, cid)
		if err != nil {
			res.Issues = append(res.Issues, VerifyIssue{
				Code:     "bad-course-index",
				CourseID: cid,
				Message:  fmt.Sprintf("parse %s: %v", filepath.Join(dir, courseIndexFileName), err),
			})
			continue
		}
		for _, t := range ci.Topics {
			if t == nil {
				continue
			}
			res.CheckedTopics++
			if t.Current != "" {
				name := t.Current + blobExt
				ok := checkBlob(MetadataBlobPath(root, t.Current), name,
					"missing-metadata-blob", "unreadable-metadata-blob",
					fmt.Sprintf("current metadata pointer %s does not exist on disk", name),
					cid, t.TopicID)
				if ok {
					seenBlobs[name] = struct{}{}
				}
			}
			for _, v := range t.Versions {
				name := v.UUID + blobExt
				checkBlob(MetadataBlobPath(root, v.UUID), name,
					"dangling-metadata-version", "unreadable-metadata-version",
					fmt.Sprintf("versions[] entry %s not found on disk", name),
					cid, t.TopicID)
			}
			if t.CurrentBody != "" {
				name := t.CurrentBody + bodyExt
				path := BodyPath(root, t.CurrentBody)
				ok := checkBlob(path, name,
					"missing-body-blob", "unreadable-body-blob",
					fmt.Sprintf("currentBody %s does not exist on disk", name),
					cid, t.TopicID)
				if ok {
					seenBlobs[name] = struct{}{}
					if deep {
						if err := verifyBodyHash(path, t.CurrentBody); err != nil {
							res.Issues = append(res.Issues, VerifyIssue{
								Code:     "body-hash-mismatch",
								CourseID: cid, TopicID: t.TopicID, Blob: name,
								Message: err.Error(),
							})
						}
					}
				}
			}
			for _, b := range t.BodyVersions {
				name := b.SHA256 + bodyExt
				checkBlob(BodyPath(root, b.SHA256), name,
					"dangling-body-version", "unreadable-body-version",
					fmt.Sprintf("bodyVersions[] entry %s not found on disk", name),
					cid, t.TopicID)
			}
		}
	}

	entries, err := os.ReadDir(blobsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			res.CheckedBlobs++
			if _, seen := seenBlobs[name]; !seen {
				res.Orphans = append(res.Orphans, name)
			}
		}
	}

	res.OK = len(res.Issues) == 0
	return res, nil
}

func verifyBodyHash(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("body hash mismatch: filename %s != content %s", want, got)
	}
	return nil
}
