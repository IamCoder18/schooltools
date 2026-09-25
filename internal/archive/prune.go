package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PruneResult struct {
	Root           string   `json:"root"`
	BlobsBefore    int      `json:"blobsBefore"`
	BlobsAfter     int      `json:"blobsAfter"`
	Deleted        int      `json:"deleted"`
	BytesReclaim   int64    `json:"bytesReclaim"`
	Orphans        []string `json:"orphans"`
	TrimmedRecords int      `json:"trimmedRecords"`
}

func PrunePlan(root string) (PruneResult, error) {
	return prune(root, false)
}

func Prune(root string) (PruneResult, error) {
	return prune(root, true)
}

func prune(root string, apply bool) (PruneResult, error) {
	res := PruneResult{Root: root}
	blobsDir := filepath.Join(root, blobsDirName)
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			if apply {
				n, terr := trimDanglingRecords(root, map[string]struct{}{})
				res.TrimmedRecords = n
				if terr != nil {
					return res, terr
				}
				idx, idxErr := LoadIndex(root)
				if idxErr != nil {
					return res, fmt.Errorf("archive: bump index version (blobs dir was absent, trim may have rewritten indexes; load index: %w)", idxErr)
				}
				idx.Version = SchemaVersion
				idx.UpdatedAt = time.Now().UTC()
				if saveErr := SaveIndex(root, idx); saveErr != nil {
					return res, fmt.Errorf("archive: bump index version: %w", saveErr)
				}
			}
			return res, nil
		}
		return res, fmt.Errorf("archive: read blobs dir: %w", err)
	}

	live, err := collectLiveKeys(root)
	if err != nil {
		return res, err
	}

	res.BlobsBefore = len(entries)
	keep := make(map[string]struct{}, len(live))
	type victim struct {
		name string
		size int64
	}
	var victims []victim
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		key, _, ok := blobKey(name)
		if !ok {
			continue
		}
		if _, isLive := live[key]; isLive {
			keep[name] = struct{}{}
			continue
		}
		info, _ := e.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		victims = append(victims, victim{name: name, size: size})
		res.Orphans = append(res.Orphans, name)
	}

	if apply {
		for _, v := range victims {
			path := filepath.Join(blobsDir, v.name)
			if err := os.Remove(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return res, fmt.Errorf("archive: prune %s: %w", v.name, err)
			}
			res.Deleted++
			res.BytesReclaim += v.size
		}
		res.BlobsAfter = res.BlobsBefore - res.Deleted
		n, terr := trimDanglingRecords(root, keep)
		res.TrimmedRecords = n
		if terr != nil {
			return res, terr
		}
		idx, idxErr := LoadIndex(root)
		if idxErr != nil {
			return res, fmt.Errorf("archive: bump index version after trim (load index: %w; prune may already have changed per-course indexes)", idxErr)
		}
		idx.Version = SchemaVersion
		idx.UpdatedAt = time.Now().UTC()
		if saveErr := SaveIndex(root, idx); saveErr != nil {
			return res, fmt.Errorf("archive: bump index version: %w", saveErr)
		}
		return res, nil
	}

	res.Deleted = len(victims)
	for _, v := range victims {
		res.BytesReclaim += v.size
	}
	res.BlobsAfter = res.BlobsBefore - res.Deleted
	n, terr := countDanglingRecords(root, keep)
	res.TrimmedRecords = n
	if terr != nil {
		return res, terr
	}
	return res, nil
}

func blobKey(name string) (key, kind string, ok bool) {
	switch {
	case strings.HasSuffix(name, blobExt):
		return strings.TrimSuffix(name, blobExt), "metadata", true
	case strings.HasSuffix(name, bodyExt):
		return strings.TrimSuffix(name, bodyExt), "body", true
	default:
		return "", "", false
	}
}

func collectLiveKeys(root string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	dir := filepath.Join(root, coursesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return live, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ci, err := LoadCourseIndex(root, e.Name())
		if err != nil {
			return nil, err
		}
		for _, t := range ci.Topics {
			if t == nil {
				continue
			}
			if t.Current != "" {
				live[t.Current] = struct{}{}
			}
			if t.CurrentBody != "" {
				live[t.CurrentBody] = struct{}{}
			}
		}
	}
	return live, nil
}

func trimDanglingRecords(root string, keep map[string]struct{}) (int, error) {
	dir := filepath.Join(root, coursesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	trimmed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ci, err := LoadCourseIndex(root, e.Name())
		if err != nil {
			return trimmed, err
		}
		changed := false
		for _, t := range ci.Topics {
			if t == nil {
				continue
			}
			if len(t.Versions) > 0 {
				kept := t.Versions[:0]
				for _, v := range t.Versions {
					if _, ok := keep[v.UUID+blobExt]; ok {
						kept = append(kept, v)
					} else {
						trimmed++
						changed = true
					}
				}
				t.Versions = kept
			}
			if len(t.BodyVersions) > 0 {
				kept := t.BodyVersions[:0]
				for _, b := range t.BodyVersions {
					if _, ok := keep[b.SHA256+bodyExt]; ok {
						kept = append(kept, b)
					} else {
						trimmed++
						changed = true
					}
				}
				t.BodyVersions = kept
			}
		}
		if changed {
			ci.UpdatedAt = time.Now().UTC()
			if err := SaveCourseIndex(root, e.Name(), ci); err != nil {
				return trimmed, err
			}
		}
	}
	return trimmed, nil
}

func countDanglingRecords(root string, keep map[string]struct{}) (int, error) {
	dir := filepath.Join(root, coursesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ci, err := LoadCourseIndex(root, e.Name())
		if err != nil {
			return count, err
		}
		for _, t := range ci.Topics {
			if t == nil {
				continue
			}
			for _, v := range t.Versions {
				if _, ok := keep[v.UUID+blobExt]; !ok {
					count++
				}
			}
			for _, b := range t.BodyVersions {
				if _, ok := keep[b.SHA256+bodyExt]; !ok {
					count++
				}
			}
		}
	}
	return count, nil
}
