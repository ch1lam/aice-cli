package update

import (
	"context"
	"io"

	"github.com/creativeprojects/go-selfupdate"
)

// progressSource observes the archive without changing the updater's download,
// checksum validation, extraction, or replacement path.
type progressSource struct {
	selfupdate.Source
	current string
	report  func(Progress) error
}

func (s progressSource) DownloadReleaseAsset(ctx context.Context, release *selfupdate.Release, assetID int64) (io.ReadCloser, error) {
	if s.report == nil {
		return s.Source.DownloadReleaseAsset(ctx, release, assetID)
	}
	p := Progress{Current: s.current, Latest: release.Version(), Total: int64(release.AssetByteSize)}
	if assetID != release.AssetID {
		p.Verifying = true
	}
	if err := s.report(p); err != nil {
		return nil, err
	}
	reader, err := s.Source.DownloadReleaseAsset(ctx, release, assetID)
	if err != nil {
		return nil, err
	}
	if p.Verifying {
		return reader, nil
	}
	return &progressReader{ReadCloser: reader, progress: p, report: s.report}, nil
}

type progressReader struct {
	io.ReadCloser
	progress Progress
	report   func(Progress) error
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.progress.Downloaded += int64(n)
		if reportErr := r.report(r.progress); reportErr != nil {
			return n, reportErr
		}
	}
	return n, err
}
