package strm

import (
	"path/filepath"
	"strings"
)

type classifiedScanFile struct {
	media       mediaCandidate
	metadata    metadataItem
	hasMedia    bool
	hasMetadata bool
}

func classifyScanFile(
	fileID, fileName, outputFolder string,
	size int64,
	relDirs []string,
	exts, metaExts map[string]struct{},
	minMediaBytes, metaMaxBytes int64,
	syncMetadata bool,
) classifiedScanFile {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if _, ok := exts[ext]; ok {
		if minMediaBytes > 0 && size < minMediaBytes {
			return classifiedScanFile{}
		}
		return classifiedScanFile{
			media: mediaCandidate{
				fileID:   fileID,
				fileName: fileName,
				size:     size,
				relDirs:  append([]string{}, relDirs...),
			},
			hasMedia: true,
		}
	}
	if syncMetadata && len(metaExts) > 0 {
		if _, ok := metaExts[ext]; ok {
			if metaMaxBytes > 0 && size > metaMaxBytes {
				return classifiedScanFile{}
			}
			return classifiedScanFile{
				metadata:    newMetadataItem(fileID, fileName, outputFolder, relDirs),
				hasMetadata: true,
			}
		}
	}
	return classifiedScanFile{}
}
