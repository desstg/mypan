package uploadutil

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"os"

	"litepan/internal/domain"
)

func HashMD5(ctx context.Context, path string) (string, error) {
	h := md5.New()
	if err := readFileHashes(ctx, path, h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func HashMD5SHA1(ctx context.Context, path string) (string, string, error) {
	hMD5 := md5.New()
	hSHA1 := sha1.New()
	if err := readFileHashes(ctx, path, hMD5, hSHA1); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(hMD5.Sum(nil)), hex.EncodeToString(hSHA1.Sum(nil)), nil
}

func readFileHashes(ctx context.Context, path string, writers ...io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()

	buf := make([]byte, DefaultReadChunk)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			for _, w := range writers {
				if _, werr := w.Write(chunk); werr != nil {
					return domain.Wrap(domain.CodeDriverError, werr)
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return domain.Wrap(domain.CodeDriverError, readErr)
		}
	}
}
